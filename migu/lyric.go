package migu

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/guohuiyuan/music-lib/model"
	"github.com/guohuiyuan/music-lib/utils"
	"net/url"
	"strings"
)

func GetLyrics(s *model.Song) (string, error) { return defaultMigu.GetLyrics(s) }

func (m *Migu) GetLyrics(s *model.Song) (string, error) {
	if s.Source != "migu" {
		return "", errors.New("source mismatch")
	}

	contentID := ""
	if s.Extra != nil && s.Extra["content_id"] != "" {
		contentID = s.Extra["content_id"]
	} else {
		parts := strings.Split(s.ID, "|")
		if len(parts) >= 1 {
			contentID = parts[0]
		}
	}

	if contentID == "" {
		return "", errors.New("invalid migu song id")
	}

	params := url.Values{}
	params.Set("resourceId", contentID)
	params.Set("resourceType", "2")

	apiURL := miguResourceInfoAPI + "?" + params.Encode()

	body, err := utils.Get(apiURL,
		miguAppRequestOptions(m.cookie)...,
	)
	if err != nil {
		return "", err
	}

	var resp struct {
		Resource []struct {
			LrcUrl   string `json:"lrcUrl"`
			LyricUrl string `json:"lyricUrl"`
		} `json:"resource"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("migu resource info parse error: %w", err)
	}

	if len(resp.Resource) == 0 {
		return "", errors.New("resource info not found")
	}

	lyricUrl := resp.Resource[0].LrcUrl
	if lyricUrl == "" {
		lyricUrl = resp.Resource[0].LyricUrl
	}

	if lyricUrl == "" {
		return "", errors.New("lyric url not found")
	}

	lyricUrl = strings.Replace(lyricUrl, "http://", "https://", 1)

	lrcBody, err := utils.Get(lyricUrl, miguAppRequestOptions(m.cookie)...)
	if err != nil {
		return "", fmt.Errorf("download lyric failed: %w", err)
	}

	return string(lrcBody), nil
}
