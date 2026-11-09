package migu

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/guohuiyuan/music-lib/model"
)

func SearchAlbum(keyword string) ([]model.Playlist, error) {
	return defaultMigu.SearchAlbum(keyword)
}

func GetAlbumSongs(id string) ([]model.Song, error) { return defaultMigu.GetAlbumSongs(id) }

func ParseAlbum(link string) (*model.Playlist, []model.Song, error) {
	return defaultMigu.ParseAlbum(link)
}

type miguAlbumSearchItem struct {
	ID           string          `json:"id"`
	ResourceType string          `json:"resourceType"`
	Name         string          `json:"name"`
	Singer       string          `json:"singer"`
	PublishDate  string          `json:"publishDate"`
	Desc         string          `json:"desc"`
	ImgItems     []miguImageItem `json:"imgItems"`
}

// SearchAlbum 搜索专辑
func (m *Migu) SearchAlbum(keyword string) ([]model.Playlist, error) {
	const searchSwitch = `{"song":0,"album":1,"singer":0,"tagSong":0,"mvSong":0,"songlist":0,"bestShow":1}`

	seen := make(map[string]struct{})
	albums := make([]model.Playlist, 0, miguSearchPageSize)

	err := m.searchPages(keyword, searchSwitch, func(body []byte) (int, int, error) {
		var resp struct {
			AlbumResultData struct {
				TotalCount json.Number           `json:"totalCount"`
				Result     []miguAlbumSearchItem `json:"result"`
			} `json:"albumResultData"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return 0, 0, fmt.Errorf("migu album json parse error: %w", err)
		}
		for _, item := range resp.AlbumResultData.Result {
			albumID := strings.TrimSpace(item.ID)
			if albumID == "" {
				continue
			}
			if _, ok := seen[albumID]; ok {
				continue
			}
			seen[albumID] = struct{}{}
			albums = append(albums, convertAlbumSearchItem(item, albumID))
		}
		return len(resp.AlbumResultData.Result), miguTotalCount(resp.AlbumResultData.TotalCount), nil
	})
	if err != nil {
		return nil, err
	}
	if len(albums) == 0 {
		return nil, errors.New("no albums found")
	}
	return albums, nil
}

func convertAlbumSearchItem(item miguAlbumSearchItem, albumID string) model.Playlist {
	description := strings.TrimSpace(item.Desc)
	if description == "" {
		description = strings.TrimSpace(item.PublishDate)
	}

	return model.Playlist{
		Source:      "migu",
		ID:          albumID,
		Name:        strings.TrimSpace(item.Name),
		Cover:       pickMiguImage(item.ImgItems),
		Creator:     strings.TrimSpace(item.Singer),
		Description: description,
		Link:        miguAlbumLink(albumID),
		Extra: map[string]string{
			"type":          "album",
			"album_id":      albumID,
			"resource_type": firstNonEmpty(strings.TrimSpace(item.ResourceType), "2003"),
			"publish_date":  strings.TrimSpace(item.PublishDate),
		},
	}
}

// GetAlbumSongs 获取专辑歌曲列表
func (m *Migu) GetAlbumSongs(id string) ([]model.Song, error) {
	songs, _, err := m.fetchAlbumSongs(id)
	return songs, err
}

func (m *Migu) ParseAlbum(link string) (*model.Playlist, []model.Song, error) {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`music\.migu\.cn/(?:v3|v5)/music/album/(\d+)`),
		regexp.MustCompile(`albumId=(\d+)`),
		regexp.MustCompile(`resourceId=(\d+)`),
	}

	for _, pattern := range patterns {
		matches := pattern.FindStringSubmatch(link)
		if len(matches) >= 2 {
			return m.fetchAlbumDetail(matches[1])
		}
	}

	return nil, nil, errors.New("invalid migu album link")
}
