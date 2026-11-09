package migu

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/guohuiyuan/music-lib/model"
	"github.com/guohuiyuan/music-lib/utils"
)

const (
	UserAgent   = "Mozilla/5.0 (iPhone; CPU iPhone OS 9_1 like Mac OS X) AppleWebKit/601.1.46 (KHTML, like Gecko) Version/9.0 Mobile/13B143 Safari/601.1"
	Referer     = "http://music.migu.cn/"
	MagicUserID = "15548614588710179085069"
)

type Migu struct {
	cookie string
}

var (
	miguSearchAPI       = "https://jadeite.migu.cn/music_search/v3/search/searchAll"
	miguResourceInfoAPI = "https://app.c.nf.migu.cn/MIGUM2.0/v1.0/content/resourceinfo.do"
	miguAlbumSongAPI    = "https://app.c.nf.migu.cn/MIGUM3.0/resource/album/song/v2.0"
)

const (
	// miguSearchPageSize 客户端搜索接口单页上限为 100。
	miguSearchPageSize = 100
	// miguSearchMaxPages 搜索最多翻页数，用于限制单次搜索的请求次数。
	miguSearchMaxPages = 3

	miguSearchUIVersion = "A_music_3.6.1"
	miguSearchDeviceID  = "963B7AA0D21511ED807EE5846EC87D20"
	miguSearchSignSalt  = "6cdc72a439cef99a3418d2a78aa28c73yyapp2d16148780a1dcc7408e06336b98cfd50"
	miguSearchUserAgent = "okhttp/4.9.1"
)

// searchPages 先请求第一页取得总数，再并发拉取剩余页（最多 miguSearchMaxPages 页），按页序交给 parse。
func (m *Migu) searchPages(keyword, searchSwitch string, parse func(body []byte) (pageCount int, total int, err error)) error {
	body, err := m.searchRequest(keyword, searchSwitch, 1)
	if err != nil {
		return err
	}
	pageCount, total, err := parse(body)
	if err != nil {
		return err
	}
	if pageCount < miguSearchPageSize || total <= pageCount {
		return nil
	}
	pages := (total + miguSearchPageSize - 1) / miguSearchPageSize
	if pages > miguSearchMaxPages {
		pages = miguSearchMaxPages
	}
	bodies := make([][]byte, pages+1)
	var wg sync.WaitGroup
	for pageNo := 2; pageNo <= pages; pageNo++ {
		wg.Add(1)
		go func(pageNo int) {
			defer wg.Done()
			bodies[pageNo], _ = m.searchRequest(keyword, searchSwitch, pageNo)
		}(pageNo)
	}
	wg.Wait()
	for pageNo := 2; pageNo <= pages; pageNo++ {
		if bodies[pageNo] == nil {
			return nil
		}
		if _, _, err := parse(bodies[pageNo]); err != nil {
			return nil
		}
	}
	return nil
}

func miguTotalCount(n json.Number) int {
	total, _ := strconv.Atoi(n.String())
	return total
}

func (m *Migu) searchRequest(keyword, searchSwitch string, pageNo int) ([]byte, error) {
	return m.searchRequestSized(keyword, searchSwitch, pageNo, miguSearchPageSize)
}

func (m *Migu) searchRequestSized(keyword, searchSwitch string, pageNo, pageSize int) ([]byte, error) {
	if pageSize < 1 || pageSize > miguSearchPageSize {
		pageSize = miguSearchPageSize
	}
	params := url.Values{}
	params.Set("isCorrect", "1")
	params.Set("isCopyright", "1")
	params.Set("searchSwitch", searchSwitch)
	params.Set("text", keyword)
	params.Set("pageNo", strconv.Itoa(pageNo))
	params.Set("pageSize", strconv.Itoa(pageSize))
	params.Set("sort", "1")
	params.Set("sid", "USS")

	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	sum := md5.Sum([]byte(keyword + miguSearchSignSalt + miguSearchDeviceID + timestamp))

	return utils.Get(miguSearchAPI+"?"+params.Encode(),
		utils.WithHeader("User-Agent", miguSearchUserAgent),
		utils.WithHeader("uiVersion", miguSearchUIVersion),
		utils.WithHeader("deviceId", miguSearchDeviceID),
		utils.WithHeader("timestamp", timestamp),
		utils.WithHeader("sign", hex.EncodeToString(sum[:])),
		utils.WithHeader("Cookie", m.cookie),
	)
}

func New(cookie string) *Migu { return &Migu{cookie: cookie} }

var defaultMigu = New("")

func (m *Migu) fetchPlaylistInfo(id string) (*model.Playlist, error) {
	playlistID := strings.TrimSpace(id)
	if playlistID == "" {
		return nil, errors.New("playlist id is empty")
	}

	params := url.Values{}
	params.Set("needSimple", "00")
	params.Set("resourceType", "2021")
	params.Set("resourceId", playlistID)

	apiURL := miguResourceInfoAPI + "?" + params.Encode()
	body, err := utils.Get(apiURL,
		utils.WithHeader("User-Agent", UserAgent),
		utils.WithHeader("Referer", Referer),
		utils.WithHeader("Cookie", m.cookie),
	)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Code     string `json:"code"`
		Info     string `json:"info"`
		Resource []struct {
			MusicListID    string `json:"musicListId"`
			Title          string `json:"title"`
			Summary        string `json:"summary"`
			MusicNum       int    `json:"musicNum"`
			OriginalImgURL string `json:"originalImgUrl"`
			OwnerName      string `json:"ownerName"`
			ResourceType   string `json:"resourceType"`
			ImgItem        struct {
				Img     string `json:"img"`
				ImgOri  string `json:"imgOri"`
				WebpImg string `json:"webpImg"`
			} `json:"imgItem"`
			OpNumItem struct {
				PlayNum int `json:"playNum"`
			} `json:"opNumItem"`
		} `json:"resource"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("migu playlist info json parse error: %w", err)
	}
	if resp.Code != "" && resp.Code != "000000" {
		return nil, fmt.Errorf("migu api error: %s (code %s)", resp.Info, resp.Code)
	}
	if len(resp.Resource) == 0 {
		return nil, errors.New("migu playlist info not found")
	}

	info := resp.Resource[0]
	playlistID = firstNonEmpty(info.MusicListID, playlistID)
	playlist := &model.Playlist{
		Source:      "migu",
		ID:          playlistID,
		Name:        firstNonEmpty(info.Title, playlistID),
		Cover:       firstNonEmpty(info.OriginalImgURL, info.ImgItem.Img, info.ImgItem.WebpImg, info.ImgItem.ImgOri),
		TrackCount:  info.MusicNum,
		PlayCount:   info.OpNumItem.PlayNum,
		Creator:     info.OwnerName,
		Description: info.Summary,
		Link:        miguPlaylistLink(playlistID),
		Extra: map[string]string{
			"type":          "playlist",
			"playlist_id":   playlistID,
			"resource_type": firstNonEmpty(info.ResourceType, "2021"),
		},
	}

	return playlist, nil
}

func (m *Migu) fetchPlaylistDetail(id string) (*model.Playlist, []model.Song, error) {
	playlistID := strings.TrimSpace(id)
	if playlistID == "" {
		return nil, nil, errors.New("playlist id is empty")
	}

	songs, err := m.GetPlaylistSongs(playlistID)
	if err != nil {
		return nil, nil, err
	}

	playlist, infoErr := m.fetchPlaylistInfo(playlistID)
	if infoErr != nil {
		playlist = &model.Playlist{
			Source:     "migu",
			ID:         playlistID,
			Name:       playlistID,
			TrackCount: len(songs),
			Link:       miguPlaylistLink(playlistID),
			Extra: map[string]string{
				"type":        "playlist",
				"playlist_id": playlistID,
			},
		}
	}
	if playlist.TrackCount == 0 {
		playlist.TrackCount = len(songs)
	}
	if playlist.Cover == "" && len(songs) > 0 {
		playlist.Cover = songs[0].Cover
	}

	return playlist, songs, nil
}

func (m *Migu) fetchAlbumDetail(id string) (*model.Playlist, []model.Song, error) {
	albumID := strings.TrimSpace(id)
	if albumID == "" {
		return nil, nil, errors.New("album id is empty")
	}

	songs, totalSongs, err := m.fetchAlbumSongs(albumID)
	if err != nil {
		return nil, nil, err
	}

	album, err := m.fetchAlbumInfo(albumID)
	if err != nil {
		digital, digitalErr := m.fetchDigitalAlbumInfo(albumID)
		if digitalErr != nil {
			return nil, nil, err
		}
		album = digital.toPlaylist(albumID)
	}

	if album.TrackCount == 0 {
		if totalSongs > 0 {
			album.TrackCount = totalSongs
		} else {
			album.TrackCount = len(songs)
		}
	}
	if album.Cover == "" && len(songs) > 0 {
		album.Cover = songs[0].Cover
	}

	return album, songs, nil
}

func (m *Migu) fetchAlbumInfo(albumID string) (*model.Playlist, error) {
	params := url.Values{}
	params.Set("needSimple", "00")
	params.Set("resourceType", "2003")
	params.Set("resourceId", albumID)

	apiURL := miguResourceInfoAPI + "?" + params.Encode()
	body, err := utils.Get(apiURL,
		utils.WithHeader("User-Agent", UserAgent),
		utils.WithHeader("Referer", Referer),
		utils.WithHeader("Cookie", m.cookie),
	)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Code     string `json:"code"`
		Info     string `json:"info"`
		Resource []struct {
			ResourceType   string          `json:"resourceType"`
			AlbumID        string          `json:"albumId"`
			ImgItems       []miguImageItem `json:"imgItems"`
			Title          string          `json:"title"`
			Singer         string          `json:"singer"`
			Summary        string          `json:"summary"`
			TotalCount     string          `json:"totalCount"`
			PublishTime    string          `json:"publishTime"`
			PublishCorp    string          `json:"publishCorp"`
			AlbumAliasName string          `json:"albumAliasName"`
			AlbumClass     string          `json:"albumClass"`
			Language       string          `json:"language"`
			PublishCompany string          `json:"publishCompany"`
			PublishDate    string          `json:"publishDate"`
			TranslateName  string          `json:"translateName"`
		} `json:"resource"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("migu album detail json parse error: %w", err)
	}
	if resp.Code != "" && resp.Code != "000000" {
		return nil, fmt.Errorf("migu api error: %s (code %s)", resp.Info, resp.Code)
	}
	if len(resp.Resource) == 0 {
		return nil, errors.New("album not found")
	}

	info := resp.Resource[0]
	albumID = firstNonEmpty(strings.TrimSpace(info.AlbumID), albumID)
	trackCount, _ := strconv.Atoi(strings.TrimSpace(info.TotalCount))

	return &model.Playlist{
		Source:      "migu",
		ID:          albumID,
		Name:        strings.TrimSpace(info.Title),
		Cover:       pickMiguImage(info.ImgItems),
		TrackCount:  trackCount,
		Creator:     strings.TrimSpace(info.Singer),
		Description: strings.TrimSpace(info.Summary),
		Link:        miguAlbumLink(albumID),
		Extra: map[string]string{
			"type":            "album",
			"album_id":        albumID,
			"resource_type":   firstNonEmpty(strings.TrimSpace(info.ResourceType), "2003"),
			"publish_time":    strings.TrimSpace(info.PublishTime),
			"publish_date":    strings.TrimSpace(info.PublishDate),
			"publish_corp":    strings.TrimSpace(info.PublishCorp),
			"publish_company": strings.TrimSpace(info.PublishCompany),
			"album_alias":     strings.TrimSpace(info.AlbumAliasName),
			"album_class":     strings.TrimSpace(info.AlbumClass),
			"language":        strings.TrimSpace(info.Language),
			"translate_name":  strings.TrimSpace(info.TranslateName),
		},
	}, nil
}

// miguDigitalAlbumInfo 数字专辑（resourceType=5）信息，MaterialID 为其对应的普通专辑 ID。
type miguDigitalAlbumInfo struct {
	MaterialID  string
	Title       string
	Singer      string
	Summary     string
	TotalCount  int
	PublishDate string
	Cover       string
}

func (d *miguDigitalAlbumInfo) toPlaylist(albumID string) *model.Playlist {
	return &model.Playlist{
		Source:      "migu",
		ID:          albumID,
		Name:        d.Title,
		Cover:       d.Cover,
		TrackCount:  d.TotalCount,
		Creator:     d.Singer,
		Description: d.Summary,
		Link:        miguAlbumLink(albumID),
		Extra: map[string]string{
			"type":          "album",
			"album_id":      albumID,
			"material_id":   d.MaterialID,
			"resource_type": "5",
			"publish_date":  d.PublishDate,
		},
	}
}

func (m *Migu) fetchDigitalAlbumInfo(albumID string) (*miguDigitalAlbumInfo, error) {
	params := url.Values{}
	params.Set("needSimple", "00")
	params.Set("resourceType", "5")
	params.Set("resourceId", albumID)

	body, err := utils.Get(miguResourceInfoAPI+"?"+params.Encode(),
		utils.WithHeader("User-Agent", UserAgent),
		utils.WithHeader("Referer", Referer),
		utils.WithHeader("Cookie", m.cookie),
	)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Code     string `json:"code"`
		Info     string `json:"info"`
		Resource []struct {
			MaterialID  string          `json:"materialId"`
			Title       string          `json:"title"`
			Singer      string          `json:"singer"`
			Summary     string          `json:"summary"`
			TotalCount  string          `json:"totalCount"`
			PublishDate string          `json:"publishDate"`
			ImgItem     []miguImageItem `json:"imgItem"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("migu digital album json parse error: %w", err)
	}
	if resp.Code != "" && resp.Code != "000000" {
		return nil, fmt.Errorf("migu api error: %s (code %s)", resp.Info, resp.Code)
	}
	if len(resp.Resource) == 0 {
		return nil, errors.New("digital album not found")
	}

	info := resp.Resource[0]
	totalCount, _ := strconv.Atoi(strings.TrimSpace(info.TotalCount))
	return &miguDigitalAlbumInfo{
		MaterialID:  strings.TrimSpace(info.MaterialID),
		Title:       strings.TrimSpace(info.Title),
		Singer:      strings.TrimSpace(info.Singer),
		Summary:     strings.TrimSpace(info.Summary),
		TotalCount:  totalCount,
		PublishDate: strings.TrimSpace(info.PublishDate),
		Cover:       normalizeMiguImageURL(pickMiguImage(info.ImgItem)),
	}, nil
}

// fetchAlbumSongs 获取专辑全部歌曲；数字专辑 ID 本身查不到歌曲时，改用其对应的普通专辑 ID 重查。
func (m *Migu) fetchAlbumSongs(id string) ([]model.Song, int, error) {
	albumID := strings.TrimSpace(id)
	if albumID == "" {
		return nil, 0, errors.New("album id is empty")
	}

	songs, totalCount, err := m.fetchAlbumSongPages(albumID)
	if err == nil && len(songs) > 0 {
		return songs, totalCount, nil
	}

	digital, digitalErr := m.fetchDigitalAlbumInfo(albumID)
	if digitalErr != nil || digital.MaterialID == "" || digital.MaterialID == albumID {
		if err != nil {
			return nil, totalCount, err
		}
		return nil, totalCount, errors.New("album has no playable songs")
	}
	return m.fetchAlbumSongPages(digital.MaterialID)
}

func (m *Migu) fetchAlbumSongPages(albumID string) ([]model.Song, int, error) {
	const pageSize = 50

	seen := make(map[string]struct{})
	songs := make([]model.Song, 0, pageSize)
	totalCount := 0

	for pageNo := 1; ; pageNo++ {
		params := url.Values{}
		params.Set("albumId", albumID)
		params.Set("pageNo", strconv.Itoa(pageNo))
		params.Set("pageSize", strconv.Itoa(pageSize))

		apiURL := miguAlbumSongAPI + "?" + params.Encode()
		body, err := utils.Get(apiURL,
			utils.WithHeader("User-Agent", UserAgent),
			utils.WithHeader("Referer", Referer),
			utils.WithHeader("Cookie", m.cookie),
		)
		if err != nil {
			return nil, 0, err
		}

		var resp struct {
			Code string `json:"code"`
			Info string `json:"info"`
			Data struct {
				SongList   []MiguSongItem `json:"songList"`
				TotalCount json.Number    `json:"totalCount"`
			} `json:"data"`
		}

		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, 0, fmt.Errorf("migu album songs json parse error: %w", err)
		}
		if resp.Code != "" && resp.Code != "000000" {
			return nil, 0, fmt.Errorf("migu api error: %s (code %s)", resp.Info, resp.Code)
		}

		if totalCount == 0 {
			totalCount = miguTotalCount(resp.Data.TotalCount)
		}
		if len(resp.Data.SongList) == 0 {
			break
		}

		before := len(songs)
		for _, item := range resp.Data.SongList {
			song := m.convertItemToSong(item)
			if song == nil {
				continue
			}

			key := firstNonEmpty(song.Extra["content_id"], item.ContentID, song.ID, item.CopyrightID)
			if key == "" {
				key = song.Name + "|" + song.Artist
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			songs = append(songs, *song)
		}

		if len(resp.Data.SongList) < pageSize {
			break
		}
		if totalCount > 0 && len(songs) >= totalCount {
			break
		}
		if len(songs) == before {
			break
		}
	}

	if len(songs) == 0 {
		return nil, totalCount, errors.New("album has no playable songs")
	}

	return songs, totalCount, nil
}

// 内部结构体定义，用于 Search 和 Parse 复用
type miguImageItem struct {
	ImgSizeType string `json:"imgSizeType"`
	Img         string `json:"img"`
}

type miguArtistItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type miguAlbumItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type miguRateFormat struct {
	FormatType      string   `json:"formatType"`
	ResourceType    string   `json:"resourceType"`
	Size            string   `json:"size"`
	AndroidSize     string   `json:"androidSize"`
	ISize           string   `json:"isize"`
	ASize           string   `json:"asize"`
	FileType        string   `json:"fileType"`
	AndroidFileType string   `json:"androidFileType"`
	IFormat         string   `json:"iformat"`
	AFormat         string   `json:"aformat"`
	Price           string   `json:"price"`
	ShowTag         []string `json:"showTag"`
	ShowTags        []string `json:"showTags"`
}

type miguZ3DCode struct {
	FormatType      string `json:"formatType"`
	ResourceType    string `json:"resourceType"`
	Size            string `json:"size"`
	AndroidSize     string `json:"androidSize"`
	ISize           string `json:"isize"`
	ASize           string `json:"asize"`
	FileType        string `json:"fileType"`
	AndroidFileType string `json:"androidFileType"`
	IFormat         string `json:"iformat"`
	AFormat         string `json:"aformat"`
	Price           string `json:"price"`
	Z3DKey          string `json:"z3dKey"`
	Z3DIV           string `json:"z3dIv"`
	AndroidFileKey  string `json:"androidFileKey"`
	IOSFileKey      string `json:"iosFileKey"`
	IOSSize         string `json:"iosSize"`
	IOSFileType     string `json:"iosFileType"`
	URL             string `json:"url"`
	IOSURL          string `json:"iosUrl"`
	AndroidURL      string `json:"androidUrl"`
	H5URL           string `json:"h5Url"`
}

type MiguSongItem struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	SongName        string           `json:"songName"`
	SongID          string           `json:"songId"`
	Singers         []miguArtistItem `json:"singers"`
	Artists         []miguArtistItem `json:"artists"`
	SingerList      []miguArtistItem `json:"singerList"`
	Albums          []miguAlbumItem  `json:"albums"`
	AlbumID         string           `json:"albumId"`
	Album           string           `json:"album"`
	Singer          string           `json:"singer"`
	ContentID       string           `json:"contentId"`
	CopyrightID     string           `json:"copyrightId"`
	ChargeAuditions string           `json:"chargeAuditions"`
	DownloadTags    []string         `json:"downloadTags"`
	ImgItems        []miguImageItem  `json:"imgItems"`
	AlbumImgs       []miguImageItem  `json:"albumImgs"`
	NewRateFormats  []miguRateFormat `json:"newRateFormats"`
	RateFormats     []miguRateFormat `json:"rateFormats"`
	AudioFormats    []miguRateFormat `json:"audioFormats"`
	Z3DCode         miguZ3DCode      `json:"z3dCode"`
	Img1            string           `json:"img1"`
	Img2            string           `json:"img2"`
	Img3            string           `json:"img3"`
	Duration        int              `json:"duration"`
}

// fetchSongDetail 通过 contentId 获取歌曲详情
func (m *Migu) fetchSongDetail(contentID string) (*model.Song, error) {
	params := url.Values{}
	params.Set("resourceType", "2")
	params.Set("resourceId", contentID)

	apiURL := miguResourceInfoAPI + "?" + params.Encode()
	body, err := utils.Get(apiURL,
		utils.WithHeader("User-Agent", UserAgent),
		utils.WithHeader("Referer", Referer),
		utils.WithHeader("Cookie", m.cookie),
	)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Resource []MiguSongItem `json:"resource"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}

	if len(resp.Resource) == 0 {
		return nil, errors.New("song detail not found")
	}

	song := m.convertItemToSong(resp.Resource[0])
	if song == nil {
		return nil, errors.New("no valid format found for this song")
	}
	return song, nil
}

// convertItemToSong 将 API 返回的 Item 转换为 Song 模型 (复用 Search 中的逻辑)
func (m *Migu) convertItemToSong(item MiguSongItem) *model.Song {
	return m.convertItemToSongWithOption(item, true)
}

func (m *Migu) convertItemToSongAllowPaid(item MiguSongItem) *model.Song {
	return m.convertItemToSongWithOption(item, true)
}

func (m *Migu) convertItemToSongWithOption(item MiguSongItem, allowPaid bool) *model.Song {
	artistNames := collectMiguArtistNames(item)
	songName := firstNonEmpty(strings.TrimSpace(item.Name), strings.TrimSpace(item.SongName))
	albumName := strings.TrimSpace(item.Album)
	if len(item.Albums) > 0 && strings.TrimSpace(item.Albums[0].Name) != "" {
		albumName = strings.TrimSpace(item.Albums[0].Name)
	}

	rateFormats := collectMiguFormats(item)
	if len(rateFormats) == 0 {
		return nil
	}

	type validFormat struct {
		index int
		size  int64
		ext   string
		rank  int
		vip   bool
	}
	var candidates []validFormat
	var duration int64 = int64(item.Duration)

	for i, fmtItem := range rateFormats {
		sizeStr := firstNonZeroString(fmtItem.AndroidSize, fmtItem.ASize, fmtItem.Size, fmtItem.ISize)
		sizeVal, _ := strconv.ParseInt(sizeStr, 10, 64)

		ext := firstNonEmpty(fmtItem.AndroidFileType, fmtItem.FileType)
		if ext == "" {
			ext = miguFormatExt(fmtItem.FormatType, firstNonEmpty(fmtItem.AFormat, fmtItem.IFormat))
		}

		if duration == 0 && sizeVal > 0 {
			var bitrate int64 = 0
			switch fmtItem.FormatType {
			case "PQ":
				bitrate = 128000
			case "HQ":
				bitrate = 320000
			case "LQ":
				bitrate = 64000
			}
			if bitrate > 0 {
				duration = (sizeVal * 8) / bitrate
			}
		}

		priceVal, _ := strconv.Atoi(fmtItem.Price)
		tags := fmtItem.ShowTag
		if len(tags) == 0 {
			tags = fmtItem.ShowTags
		}
		isVipTag := false
		for _, tag := range tags {
			if strings.EqualFold(strings.TrimSpace(tag), "vip") {
				isVipTag = true
				break
			}
		}
		for _, tag := range item.DownloadTags {
			if strings.EqualFold(strings.TrimSpace(tag), "vip") {
				isVipTag = true
				break
			}
		}
		isHiddenPaid := (item.ChargeAuditions == "1" && priceVal >= 200)

		// Keep paid formats in search and playlist results. Entitlement is
		// resolved by the playback endpoint, while IsVIP remains visible to callers.
		if allowPaid || (!isVipTag && !isHiddenPaid) {
			candidates = append(candidates, validFormat{
				index: i,
				size:  sizeVal,
				ext:   ext,
				rank:  miguFormatRank(fmtItem.FormatType),
				vip:   isVipTag || isHiddenPaid,
			})
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].rank != candidates[j].rank {
			return candidates[i].rank > candidates[j].rank
		}
		return candidates[i].size > candidates[j].size
	})
	bestInfo := candidates[0]
	bestFormat := rateFormats[bestInfo.index]

	displaySize := bestInfo.size

	bitrate := 0
	if duration > 0 && bestInfo.size > 0 {
		bitrate = int(bestInfo.size * 8 / 1000 / duration)
	}

	coverURL := normalizeMiguImageURL(firstNonEmpty(pickMiguImage(item.ImgItems), pickMiguImage(item.AlbumImgs), item.Img1, item.Img2, item.Img3))

	linkID := firstNonEmpty(item.ContentID, item.CopyrightID)
	extra := map[string]string{
		"content_id":    item.ContentID,
		"resource_type": bestFormat.ResourceType,
		"format_type":   bestFormat.FormatType,
	}
	if item.CopyrightID != "" {
		extra["copyright_id"] = item.CopyrightID
	}
	if item.SongID != "" {
		extra["song_id"] = item.SongID
	}
	if item.AlbumID != "" {
		extra["album_id"] = item.AlbumID
	}

	isVIP := item.ChargeAuditions == "1" || bestInfo.vip

	return &model.Song{
		Source:   "migu",
		ID:       fmt.Sprintf("%s|%s|%s", item.ContentID, bestFormat.ResourceType, bestFormat.FormatType),
		Name:     songName,
		Artist:   strings.Join(artistNames, " / "),
		Album:    albumName,
		Size:     displaySize,
		Duration: int(duration),
		Bitrate:  bitrate,
		Cover:    coverURL,
		Ext:      bestInfo.ext,
		Link:     fmt.Sprintf("https://music.migu.cn/v3/music/song/%s", linkID),
		Extra:    extra,
		IsVIP:    isVIP,
	}
}

func collectMiguFormats(item MiguSongItem) []miguRateFormat {
	groups := [][]miguRateFormat{
		item.NewRateFormats,
		item.RateFormats,
		item.AudioFormats,
	}
	if item.Z3DCode.FormatType != "" {
		groups = append(groups, []miguRateFormat{{
			FormatType:      item.Z3DCode.FormatType,
			ResourceType:    item.Z3DCode.ResourceType,
			Size:            item.Z3DCode.Size,
			AndroidSize:     item.Z3DCode.AndroidSize,
			ISize:           item.Z3DCode.ISize,
			ASize:           item.Z3DCode.ASize,
			FileType:        item.Z3DCode.FileType,
			AndroidFileType: item.Z3DCode.AndroidFileType,
			IFormat:         item.Z3DCode.IFormat,
			AFormat:         item.Z3DCode.AFormat,
			Price:           item.Z3DCode.Price,
		}})
	}

	formats := make([]miguRateFormat, 0)
	seen := make(map[string]struct{})
	for _, group := range groups {
		for _, format := range group {
			key := strings.ToUpper(strings.TrimSpace(format.FormatType)) + "|" + strings.TrimSpace(format.ResourceType)
			if key == "|" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			formats = append(formats, format)
		}
	}
	return formats
}

func miguFormatRank(formatType string) int {
	switch strings.ToUpper(strings.TrimSpace(formatType)) {
	case "ZQ32":
		return 80
	case "ZQ", "ZQ24":
		return 75
	case "SQ":
		return 70
	case "HQ":
		return 60
	case "I3D", "Z3D":
		return 50
	case "3D60":
		return 40
	case "PQ":
		return 30
	case "LQ":
		return 20
	default:
		return 10
	}
}

// GetLyrics 获取歌词
func collectMiguArtistNames(item MiguSongItem) []string {
	names := make([]string, 0, len(item.Singers)+len(item.Artists)+1)
	seen := make(map[string]struct{})

	appendName := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}

	for _, singer := range item.Singers {
		appendName(singer.Name)
	}
	for _, singer := range item.SingerList {
		appendName(singer.Name)
	}
	for _, artist := range item.Artists {
		appendName(artist.Name)
	}
	if len(names) == 0 {
		for _, name := range strings.Split(item.Singer, "|") {
			appendName(name)
		}
	}

	return names
}

func pickMiguImage(items []miguImageItem) string {
	for _, preferred := range []string{"02", "01", "03"} {
		for _, item := range items {
			if item.ImgSizeType == preferred && strings.TrimSpace(item.Img) != "" {
				return item.Img
			}
		}
	}
	for _, item := range items {
		if strings.TrimSpace(item.Img) != "" {
			return item.Img
		}
	}
	return ""
}

func firstNonZeroString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && value != "0" {
			return value
		}
	}
	return ""
}

func normalizeMiguImageURL(image string) string {
	image = strings.TrimSpace(image)
	if image == "" {
		return ""
	}
	if strings.HasPrefix(image, "http://") || strings.HasPrefix(image, "https://") {
		return image
	}
	if strings.HasPrefix(image, "//") {
		return "https:" + image
	}
	if strings.HasPrefix(image, "/") {
		return "https://d.musicapp.migu.cn" + image
	}
	return image
}

func miguFormatExt(formatType, formatCode string) string {
	formatType = strings.ToUpper(strings.TrimSpace(formatType))
	formatCode = strings.TrimSpace(formatCode)
	switch formatType {
	case "SQ", "ZQ", "ZQ24":
		return "flac"
	case "ZQ32", "Z3D", "3D60":
		return "wav"
	case "I3D":
		return "m4a"
	case "PQ", "HQ", "LQ":
		return "mp3"
	}
	if strings.HasPrefix(formatCode, "011") {
		return "flac"
	}
	return "mp3"
}

func miguAlbumLink(id string) string {
	return fmt.Sprintf("https://music.migu.cn/v3/music/album/%s", id)
}

func miguPlaylistLink(id string) string {
	return fmt.Sprintf("https://music.migu.cn/v5/#/playlist?playlistId=%s&playlistType=ordinary", id)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
