package migu

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func albumSearchPage(pageNo int) []byte {
	type item struct {
		ID           string `json:"id"`
		ResourceType string `json:"resourceType"`
		Name         string `json:"name"`
	}
	start := (pageNo - 1) * miguSearchPageSize
	items := make([]item, 0, miguSearchPageSize)
	for i := start; i < start+miguSearchPageSize && i < 41; i++ {
		items = append(items, item{ID: strconv.Itoa(1000 + i), ResourceType: "2003", Name: fmt.Sprintf("album %d", i)})
	}
	body, _ := json.Marshal(map[string]any{
		"albumResultData": map[string]any{"totalCount": "41", "result": items},
	})
	return body
}

func TestSearchAlbumFollowsPages(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		pageNo, _ := strconv.Atoi(r.URL.Query().Get("pageNo"))
		w.Write(albumSearchPage(pageNo))
	}))
	defer server.Close()

	oldAPI := miguSearchAPI
	miguSearchAPI = server.URL
	defer func() { miguSearchAPI = oldAPI }()

	albums, err := New("").SearchAlbum("test")
	if err != nil {
		t.Fatalf("SearchAlbum error: %v", err)
	}
	if len(albums) != 41 {
		t.Fatalf("expected 41 albums, got %d", len(albums))
	}
	if requests != 3 {
		t.Fatalf("expected 3 page requests, got %d", requests)
	}
	if albums[20].ID != "1020" {
		t.Fatalf("unexpected album order: %s", albums[20].ID)
	}
}

func TestFetchAlbumSongsFallsBackToDigitalAlbum(t *testing.T) {
	const digitalID = "600927015009001083"
	const materialID = "1142521343"

	mux := http.NewServeMux()
	mux.HandleFunc("/queryAlbumSong", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("albumId") != materialID {
			w.Write([]byte(`{"code":"000000","info":"操作成功","data":{}}`))
			return
		}
		w.Write([]byte(`{"code":"000000","data":{"totalCount":1,"songList":[{"contentId":"600919000007823377","songName":"太阳之子","singer":"周杰伦","albumId":"1142521343","album":"太阳之子","newRateFormats":[{"formatType":"PQ","resourceType":"2","androidSize":"3000000","androidFileType":"mp3"}]}]}}`))
	})
	mux.HandleFunc("/resourceinfo.do", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("resourceType") == "5" && q.Get("resourceId") == digitalID {
			w.Write([]byte(`{"code":"000000","resource":[{"resourceType":"5","materialId":"` + materialID + `","title":"太阳之子","singer":"周杰伦","summary":"desc","totalCount":"13","publishDate":"2026-03-25","imgItem":[{"imgSizeType":"02","img":"https://img.example/cover"}]}]}`))
			return
		}
		w.Write([]byte(`{"code":"000000","resource":[]}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	oldSong, oldInfo := miguAlbumSongAPI, miguResourceInfoAPI
	miguAlbumSongAPI = server.URL + "/queryAlbumSong"
	miguResourceInfoAPI = server.URL + "/resourceinfo.do"
	defer func() { miguAlbumSongAPI, miguResourceInfoAPI = oldSong, oldInfo }()

	songs, err := New("").GetAlbumSongs(digitalID)
	if err != nil {
		t.Fatalf("GetAlbumSongs error: %v", err)
	}
	if len(songs) != 1 || songs[0].Name != "太阳之子" {
		t.Fatalf("unexpected songs: %+v", songs)
	}

	album, songs, err := New("").ParseAlbum("https://music.migu.cn/v3/music/album/" + digitalID)
	if err != nil {
		t.Fatalf("ParseAlbum error: %v", err)
	}
	if album.Name != "太阳之子" || album.TrackCount != 13 || album.Cover != "https://img.example/cover" || album.Extra["material_id"] != materialID {
		t.Fatalf("unexpected album: %+v", album)
	}
	if len(songs) != 1 {
		t.Fatalf("expected 1 song, got %d", len(songs))
	}
}

func TestSearchFollowsPagesAndStopsAtCap(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		pageNo, _ := strconv.Atoi(r.URL.Query().Get("pageNo"))
		items := make([]map[string]any, 0, miguSearchPageSize)
		for i := 0; i < miguSearchPageSize; i++ {
			id := strconv.Itoa(pageNo*1000 + i)
			items = append(items, map[string]any{
				"contentId": id, "name": "song " + id, "singer": "artist",
				"newRateFormats": []map[string]any{{"formatType": "PQ", "resourceType": "2", "androidSize": "1000", "androidFileType": "mp3"}},
			})
		}
		body, _ := json.Marshal(map[string]any{"songResultData": map[string]any{"totalCount": "401", "result": items}})
		w.Write(body)
	}))
	defer server.Close()

	oldAPI := miguSearchAPI
	miguSearchAPI = server.URL
	defer func() { miguSearchAPI = oldAPI }()

	songs, err := New("").Search("test")
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if requests != miguSearchMaxPages {
		t.Fatalf("expected %d page requests, got %d", miguSearchMaxPages, requests)
	}
	if len(songs) != miguSearchMaxPages*miguSearchPageSize {
		t.Fatalf("expected %d songs, got %d", miguSearchMaxPages*miguSearchPageSize, len(songs))
	}
}
