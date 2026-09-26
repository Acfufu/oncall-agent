package notify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostSuccess(t *testing.T) {
	var gotBody, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	payload := []byte(`{"id":"r1","status":"done"}`)
	if err := Post(context.Background(), srv.URL, payload); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if gotBody != string(payload) {
		t.Errorf("body = %q, want %q", gotBody, payload)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type = %q, want application/json", gotCT)
	}
}

func TestPostUnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := Post(context.Background(), srv.URL, []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err = %v, want unexpected status 500", err)
	}
}

func TestPostConnRefused(t *testing.T) {
	// 死端口：网络错误原样返回，交 asynq 退避重试（F3 断网恢复语义的前提）。
	if err := Post(context.Background(), "http://127.0.0.1:1/", []byte(`{}`)); err == nil {
		t.Fatal("err = nil, want connection error")
	}
}
