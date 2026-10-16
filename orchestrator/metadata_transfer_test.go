package orchestrator

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"immich-video-optimizer/config"
	"immich-video-optimizer/engine"
	"immich-video-optimizer/immich"
)

func TestPipelineCopiesTagsAndStackBeforeDeletingOriginal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var events []string
	var deletedOriginal, rolledBack bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/assets/original/original":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("source"))
		case r.Method == http.MethodGet && r.URL.Path == "/api/albums":
			_, _ = w.Write([]byte("[]"))
		case r.Method == http.MethodGet && r.URL.Path == "/api/assets/original":
			events = append(events, "get-original")
			_, _ = w.Write([]byte(`{"id":"original","type":"VIDEO","isFavorite":false,"tags":[{"id":"tag-a","name":"A"},{"id":"tag-b","name":"B"}],"stack":{"id":"stack-1","primaryAssetId":"original"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/albums":
			_, _ = w.Write([]byte(`{"id":"tracking"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/assets":
			_, _ = w.Write([]byte(`{"id":"replacement","type":"VIDEO"}`))
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/tags/"):
			events = append(events, "tag:"+strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/tags/"), "/assets"))
			_, _ = w.Write([]byte(`[{"id":"replacement","success":true}]`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/assets/copy":
			var body struct {
				SourceID    string `json:"sourceId"`
				TargetID    string `json:"targetId"`
				Albums      bool   `json:"albums"`
				Favorite    bool   `json:"favorite"`
				SharedLinks bool   `json:"sharedLinks"`
				Sidecar     bool   `json:"sidecar"`
				Stack       bool   `json:"stack"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.SourceID != "original" || body.TargetID != "replacement" || !body.Stack || body.Albums || body.Favorite || body.SharedLinks || body.Sidecar {
				t.Errorf("unexpected stack-copy payload: %+v", body)
			}
			events = append(events, "stack")
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/albums/"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/assets":
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "original") {
				deletedOriginal = true
				events = append(events, "delete-original")
			} else if strings.Contains(string(body), "replacement") {
				rolledBack = true
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	p := newMetadataTransferTestPipeline(t, server)
	err := runMetadataTransferTestPipeline(p)
	if err != nil {
		t.Fatalf("pipeline failed: %v", err)
	}
	if !deletedOriginal || rolledBack {
		t.Fatalf("expected original deletion only; deletedOriginal=%v rolledBack=%v", deletedOriginal, rolledBack)
	}
	if strings.Join(events, ",") != "get-original,tag:tag-a,tag:tag-b,stack,delete-original" {
		t.Fatalf("unexpected transfer order: %v", events)
	}
}

func TestPipeline_TagLookupFailureKeepsOriginalBeforeUploading(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var originalDetailRead, originalDeleted, replacementUploaded bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/assets/original/original":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("source"))
		case r.Method == http.MethodGet && r.URL.Path == "/api/albums":
			_, _ = w.Write([]byte("[]"))
		case r.Method == http.MethodGet && r.URL.Path == "/api/assets/original":
			originalDetailRead = true
			w.WriteHeader(http.StatusInternalServerError)
		case r.Method == http.MethodPost && r.URL.Path == "/api/albums":
			_, _ = w.Write([]byte(`{"id":"tracking"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/assets":
			replacementUploaded = true
			_, _ = w.Write([]byte(`{"id":"replacement","type":"VIDEO"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/assets":
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "original") {
				originalDeleted = true
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	p := newMetadataTransferTestPipeline(t, server)
	err := runMetadataTransferTestPipeline(p)
	if err == nil {
		t.Fatal("expected tag lookup failure to fail the pipeline")
	}
	if !originalDetailRead || originalDeleted || replacementUploaded {
		t.Fatalf("unsafe failure handling: detailRead=%v originalDeleted=%v replacementUploaded=%v", originalDetailRead, originalDeleted, replacementUploaded)
	}
}

func newMetadataTransferTestPipeline(t *testing.T, server *httptest.Server) *Pipeline {
	t.Helper()
	tmp := t.TempDir()
	ffmpeg := filepath.Join(tmp, "ffmpeg")
	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\nfor arg do last_arg=\"$arg\"; done\necho 'small' > \"$last_arg\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	exiftool := filepath.Join(tmp, "exiftool")
	if err := os.WriteFile(exiftool, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	p, err := NewPipelineWithBinaries(&config.Config{URL: server.URL, APIKey: "test-key", ConcurrentEncoders: 1}, &engine.BinaryManager{FFmpegPath: ffmpeg, ExifToolPath: exiftool})
	if err != nil {
		t.Fatal(err)
	}
	p.Client().SetHTTPClient(&http.Client{Transport: &testRoundTripper{handler: server.Config.Handler}})
	return p
}

func runMetadataTransferTestPipeline(p *Pipeline) error {
	ch := make(chan interface{}, 32)
	var gotErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		for msg := range ch {
			if complete, ok := msg.(PipelineVideoCompleteMsg); ok && complete.Err != nil {
				gotErr = complete.Err
			}
		}
	}()
	p.Run(context.Background(), []immich.Asset{{ID: "original", Type: "VIDEO", OriginalFileName: "original.mp4", Size: 10000}}, ch)
	<-done
	return gotErr
}

func TestPipeline_TagOrStackTransferFailureRollsBackReplacement(t *testing.T) {
	for _, failingPath := range []string{"/api/tags/tag-a/assets", "/api/assets/copy"} {
		t.Run(strings.TrimPrefix(failingPath, "/api/"), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			var originalDeleted, replacementRolledBack bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/assets/original/original":
					_, _ = w.Write([]byte("source"))
				case r.Method == http.MethodGet && r.URL.Path == "/api/assets/original":
					_, _ = w.Write([]byte(`{"id":"original","type":"VIDEO","tags":[{"id":"tag-a","name":"A"}],"stack":{"id":"stack-1","primaryAssetId":"original"}}`))
				case r.Method == http.MethodGet && r.URL.Path == "/api/albums":
					_, _ = w.Write([]byte("[]"))
				case r.Method == http.MethodPost && r.URL.Path == "/api/albums":
					_, _ = w.Write([]byte(`{"id":"tracking"}`))
				case r.Method == http.MethodPost && r.URL.Path == "/api/assets":
					_, _ = w.Write([]byte(`{"id":"replacement","type":"VIDEO"}`))
				case r.Method == http.MethodPut && r.URL.Path == failingPath:
					w.WriteHeader(http.StatusInternalServerError)
				case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/tags/"):
					_, _ = w.Write([]byte(`[{"id":"replacement","success":true}]`))
				case r.Method == http.MethodPut && r.URL.Path == "/api/assets/copy":
					w.WriteHeader(http.StatusNoContent)
				case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/albums/"):
					w.WriteHeader(http.StatusOK)
				case r.Method == http.MethodDelete && r.URL.Path == "/api/assets":
					body, _ := io.ReadAll(r.Body)
					if strings.Contains(string(body), "original") {
						originalDeleted = true
					} else if strings.Contains(string(body), "replacement") {
						replacementRolledBack = true
					}
					w.WriteHeader(http.StatusOK)
				default:
					w.WriteHeader(http.StatusOK)
				}
			}))
			defer server.Close()

			p := newMetadataTransferTestPipeline(t, server)
			if err := runMetadataTransferTestPipeline(p); err == nil {
				t.Fatal("expected metadata transfer failure")
			}
			if originalDeleted || !replacementRolledBack {
				t.Fatalf("unsafe failure handling: originalDeleted=%v replacementRolledBack=%v", originalDeleted, replacementRolledBack)
			}
		})
	}
}

func TestPipelineUntaggedAndNonStackedAssetSucceeds(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var copyCalled, tagCalled, originalDeleted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/assets/original/original":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("source"))
		case r.Method == http.MethodGet && r.URL.Path == "/api/albums":
			_, _ = w.Write([]byte("[]"))
		case r.Method == http.MethodGet && r.URL.Path == "/api/assets/original":
			// Asset without tags or stack (omitted in JSON)
			_, _ = w.Write([]byte(`{"id":"original","type":"VIDEO","isFavorite":false}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/albums":
			_, _ = w.Write([]byte(`{"id":"tracking"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/assets":
			_, _ = w.Write([]byte(`{"id":"replacement","type":"VIDEO"}`))
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/tags/"):
			tagCalled = true
			_, _ = w.Write([]byte(`[{"id":"replacement","success":true}]`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/assets/copy":
			copyCalled = true
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/assets":
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "original") {
				originalDeleted = true
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	p := newMetadataTransferTestPipeline(t, server)
	if err := runMetadataTransferTestPipeline(p); err != nil {
		t.Fatalf("expected pipeline to succeed for untagged/non-stacked asset: %v", err)
	}
	if !originalDeleted {
		t.Fatal("expected original asset to be deleted after successful optimization")
	}
	if copyCalled {
		t.Fatal("unexpected call to /api/assets/copy for non-stacked asset")
	}
	if tagCalled {
		t.Fatal("unexpected call to /api/tags/ for untagged asset")
	}
}

func TestPipelineSafetyGuardErrorDescriptions(t *testing.T) {
	tests := []struct {
		name        string
		origJSON    string
		failAlbum   bool
		failFav     bool
		failTag     bool
		failStack   bool
		expectedErr string
	}{
		{
			name:        "album only",
			origJSON:    `{"id":"original","type":"VIDEO"}`,
			failAlbum:   true,
			expectedErr: "failed to transfer album membership; original asset preserved",
		},
		{
			name:        "favorite only",
			origJSON:    `{"id":"original","type":"VIDEO","isFavorite":true}`,
			failFav:     true,
			expectedErr: "failed to transfer favorite status; original asset preserved",
		},
		{
			name:        "tags only",
			origJSON:    `{"id":"original","type":"VIDEO","tags":[{"id":"tag-1","name":"T"}]}`,
			failTag:     true,
			expectedErr: "failed to transfer tags; original asset preserved",
		},
		{
			name:        "stack only",
			origJSON:    `{"id":"original","type":"VIDEO","stack":{"id":"s1","primaryAssetId":"original"}}`,
			failStack:   true,
			expectedErr: "failed to transfer stack membership; original asset preserved",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/assets/original/original":
					_, _ = w.Write([]byte("source"))
				case r.Method == http.MethodGet && r.URL.Path == "/api/assets/original":
					_, _ = w.Write([]byte(tc.origJSON))
				case r.Method == http.MethodGet && r.URL.Path == "/api/albums":
					if tc.failAlbum {
						_, _ = w.Write([]byte(`[{"id":"user-album","albumName":"Vacation"}]`))
					} else {
						_, _ = w.Write([]byte("[]"))
					}
				case r.Method == http.MethodPost && r.URL.Path == "/api/albums":
					_, _ = w.Write([]byte(`{"id":"tracking"}`))
				case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "user-album"):
					if tc.failAlbum {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					w.WriteHeader(http.StatusOK)
				case r.Method == http.MethodPost && r.URL.Path == "/api/assets":
					_, _ = w.Write([]byte(`{"id":"replacement","type":"VIDEO"}`))
				case r.Method == http.MethodPut && r.URL.Path == "/api/assets/replacement":
					if tc.failFav {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					w.WriteHeader(http.StatusOK)
				case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/tags/"):
					if tc.failTag {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					_, _ = w.Write([]byte(`[{"id":"replacement","success":true}]`))
				case r.Method == http.MethodPut && r.URL.Path == "/api/assets/copy":
					if tc.failStack {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					w.WriteHeader(http.StatusNoContent)
				case r.Method == http.MethodDelete && r.URL.Path == "/api/assets":
					w.WriteHeader(http.StatusOK)
				default:
					w.WriteHeader(http.StatusOK)
				}
			}))
			defer server.Close()

			p := newMetadataTransferTestPipeline(t, server)
			err := runMetadataTransferTestPipeline(p)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if err.Error() != tc.expectedErr {
				t.Fatalf("error mismatch:\n  got:  %q\n  want: %q", err.Error(), tc.expectedErr)
			}
		})
	}
}
