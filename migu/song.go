package migu

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/guohuiyuan/music-lib/model"
	"regexp"
)

func Search(keyword string) ([]model.Song, error) { return defaultMigu.Search(keyword) }

func Parse(link string) (*model.Song, error) { return defaultMigu.Parse(link) }

// Search 搜索歌曲
func (m *Migu) Search(keyword string) ([]model.Song, error) {
	const searchSwitch = `{"song":1,"album":0,"singer":0,"tagSong":0,"mvSong":0,"songlist":0,"bestShow":1}`

	seen := make(map[string]struct{})
	songs := make([]model.Song, 0, miguSearchPageSize)

	err := m.searchPages(keyword, searchSwitch, func(body []byte) (int, int, error) {
		var resp struct {
			SongResultData struct {
				TotalCount json.Number    `json:"totalCount"`
				Result     []MiguSongItem `json:"result"`
			} `json:"songResultData"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return 0, 0, fmt.Errorf("migu json parse error: %w", err)
		}
		for _, item := range resp.SongResultData.Result {
			song := m.convertItemToSong(item)
			if song == nil {
				continue
			}
			if _, ok := seen[song.ID]; ok {
				continue
			}
			seen[song.ID] = struct{}{}
			songs = append(songs, *song)
		}
		return len(resp.SongResultData.Result), miguTotalCount(resp.SongResultData.TotalCount), nil
	})
	if err != nil {
		return nil, err
	}
	return songs, nil
}

func (m *Migu) Parse(link string) (*model.Song, error) {
	// 1. 提取 ContentID
	// 支持格式: https://music.migu.cn/v3/music/song/60054701934
	re := regexp.MustCompile(`music\.migu\.cn/v3/music/song/(\d+)`)
	matches := re.FindStringSubmatch(link)
	if len(matches) < 2 {
		return nil, errors.New("invalid migu link")
	}
	contentID := matches[1]

	// 2. 获取歌曲详情 (为了拿到 resourceType 和 formatType)
	song, err := m.fetchSongDetail(contentID)
	if err != nil {
		return nil, err
	}

	// 3. 获取下载链接
	// 因为 convertItemToSong 已经填充了 Extra，所以可以直接调用 GetDownloadURL
	downloadURL, err := m.GetDownloadURL(song)
	if err == nil {
		song.URL = downloadURL
	}

	return song, nil
}
