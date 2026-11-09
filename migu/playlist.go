package migu

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/guohuiyuan/music-lib/model"
)

func SearchPlaylist(keyword string) ([]model.Playlist, error) {
	return defaultMigu.SearchPlaylist(keyword)
}

func GetPlaylistSongs(id string) ([]model.Song, error) { return defaultMigu.GetPlaylistSongs(id) }

func ParsePlaylist(link string) (*model.Playlist, []model.Song, error) {
	return defaultMigu.ParsePlaylist(link)
}

func GetPlaylistCategories() ([]model.PlaylistCategory, error) {
	return defaultMigu.GetPlaylistCategories()
}

func GetCategoryPlaylists(categoryID string, page, limit int) ([]model.Playlist, error) {
	return defaultMigu.GetCategoryPlaylists(categoryID, page, limit)
}

func (m *Migu) GetPlaylistCategories() ([]model.PlaylistCategory, error) {
	categories := []model.PlaylistCategory{{
		Source: "migu",
		ID:     "",
		Name:   "全部",
		Group:  "全部",
	}}
	for _, item := range miguPlaylistCategoryKeywords {
		categories = append(categories, model.PlaylistCategory{
			Source: "migu",
			ID:     item.Name,
			Name:   item.Name,
			Group:  item.Group,
			Hot:    item.Hot,
		})
	}
	return categories, nil
}

func (m *Migu) GetCategoryPlaylists(categoryID string, page, limit int) ([]model.Playlist, error) {
	categoryID = strings.TrimSpace(categoryID)
	if categoryID == "" {
		categoryID = "华语"
	}
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}

	playlists, err := m.searchPlaylists(categoryID, page, limit)
	if err != nil {
		return nil, err
	}
	for i := range playlists {
		if playlists[i].Extra == nil {
			playlists[i].Extra = map[string]string{}
		}
		playlists[i].Extra["category_id"] = categoryID
	}
	if len(playlists) == 0 {
		return nil, errors.New("no category playlists found")
	}
	return playlists, nil
}

var miguPlaylistCategoryKeywords = []struct {
	Name  string
	Group string
	Hot   bool
}{
	{Name: "华语", Group: "语种", Hot: true},
	{Name: "欧美", Group: "语种", Hot: true},
	{Name: "日语", Group: "语种"},
	{Name: "韩语", Group: "语种"},
	{Name: "粤语", Group: "语种"},
	{Name: "流行", Group: "风格", Hot: true},
	{Name: "摇滚", Group: "风格", Hot: true},
	{Name: "民谣", Group: "风格", Hot: true},
	{Name: "电子", Group: "风格"},
	{Name: "说唱", Group: "风格"},
	{Name: "古风", Group: "风格"},
	{Name: "轻音乐", Group: "风格"},
	{Name: "影视", Group: "场景", Hot: true},
	{Name: "ACG", Group: "场景"},
	{Name: "治愈", Group: "场景"},
	{Name: "运动", Group: "场景"},
	{Name: "学习", Group: "场景"},
	{Name: "睡前", Group: "场景"},
}

const miguPlaylistSearchSwitch = `{"song":0,"album":0,"singer":0,"tagSong":0,"mvSong":0,"songlist":1,"bestShow":1}`

func (m *Migu) SearchPlaylist(keyword string) ([]model.Playlist, error) {
	seen := make(map[string]struct{})
	playlists := make([]model.Playlist, 0, miguSearchPageSize)

	err := m.searchPages(keyword, miguPlaylistSearchSwitch, func(body []byte) (int, int, error) {
		items, total, err := parseMiguPlaylistSearch(body)
		if err != nil {
			return 0, 0, err
		}
		for _, playlist := range items {
			if _, ok := seen[playlist.ID]; ok {
				continue
			}
			seen[playlist.ID] = struct{}{}
			playlists = append(playlists, playlist)
		}
		return len(items), total, nil
	})
	if err != nil {
		return nil, err
	}
	return playlists, nil
}

func (m *Migu) searchPlaylists(keyword string, page, limit int) ([]model.Playlist, error) {
	body, err := m.searchRequestSized(keyword, miguPlaylistSearchSwitch, page, limit)
	if err != nil {
		return nil, err
	}
	playlists, _, err := parseMiguPlaylistSearch(body)
	return playlists, err
}

// parseMiguPlaylistSearch 解析歌单搜索单页响应，返回歌单列表与接口总数。
func parseMiguPlaylistSearch(body []byte) ([]model.Playlist, int, error) {
	var resp struct {
		SongListResultData struct {
			TotalCount json.Number `json:"totalCount"`
			Result     []struct {
				ID              string          `json:"id"`
				Name            string          `json:"name"`
				MusicNum        string          `json:"musicNum"`
				UserName        string          `json:"userName"`
				OwnerName       string          `json:"ownerName"`
				MusicListPicURL string          `json:"musicListPicUrl"`
				PlayNum         string          `json:"playNum"`
				ResourceType    string          `json:"resourceType"`
				ImgItems        []miguImageItem `json:"imgItems"`
			} `json:"result"`
		} `json:"songListResultData"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, 0, fmt.Errorf("migu playlist json parse error: %w", err)
	}

	playlists := make([]model.Playlist, 0, len(resp.SongListResultData.Result))
	for _, item := range resp.SongListResultData.Result {
		playlistID := strings.TrimSpace(item.ID)
		name := strings.TrimSpace(item.Name)
		if playlistID == "" || name == "" {
			continue
		}
		trackCount, _ := strconv.Atoi(item.MusicNum)
		playCount, _ := strconv.Atoi(item.PlayNum)
		cover := firstNonEmpty(item.MusicListPicURL, pickMiguImage(item.ImgItems))

		playlists = append(playlists, model.Playlist{
			Source:     "migu",
			ID:         playlistID,
			Name:       name,
			Cover:      cover,
			TrackCount: trackCount,
			PlayCount:  playCount,
			Creator:    firstNonEmpty(item.UserName, item.OwnerName),
			Link:       miguPlaylistLink(playlistID),
			Extra: map[string]string{
				"type":          "playlist",
				"playlist_id":   playlistID,
				"resource_type": firstNonEmpty(item.ResourceType, "2021"),
			},
		})
	}
	return playlists, miguTotalCount(resp.SongListResultData.TotalCount), nil
}

func (m *Migu) GetPlaylistSongs(id string) ([]model.Song, error) {
	playlistID := strings.TrimSpace(id)
	if playlistID == "" {
		return nil, errors.New("playlist id is empty")
	}
	songs, _, err := m.fetchSongPages(func(pageNo int) (miguSongPage, error) {
		params := url.Values{}
		params.Set("playlistId", playlistID)
		return m.fetchSongPage(miguPlaylistSongAPI, params, pageNo, "playlist")
	}, m.convertItemToSongAllowPaid)
	if err != nil {
		return nil, err
	}
	if len(songs) == 0 {
		return nil, errors.New("playlist has no playable songs")
	}
	return songs, nil
}

func (m *Migu) ParsePlaylist(link string) (*model.Playlist, []model.Song, error) {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`playlistId=(\d+)`),
		regexp.MustCompile(`musicListId=(\d+)`),
		regexp.MustCompile(`(?:playlist|songlist)/(\d+)`),
	}

	for _, pattern := range patterns {
		matches := pattern.FindStringSubmatch(link)
		if len(matches) >= 2 {
			return m.fetchPlaylistDetail(matches[1])
		}
	}

	if len(link) > 0 && !strings.Contains(link, "/") {
		return m.fetchPlaylistDetail(link)
	}

	return nil, nil, errors.New("invalid migu playlist link")
}
