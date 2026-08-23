package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEscape(t *testing.T) {
	in := `a<b>&"c"`
	got := Escape(in)
	if strings.Contains(got, "<") || strings.Contains(got, ">") {
		t.Fatalf("unescaped brackets: %q", got)
	}
	if !strings.Contains(got, "&amp;") || !strings.Contains(got, "&lt;") {
		t.Fatalf("escape = %q", got)
	}
}

func TestGetMeAndSend(t *testing.T) {
	var gotSend map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = io.WriteString(w, `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"舟","username":"qingzhou_bot"}}`)
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			_ = json.NewDecoder(r.Body).Decode(&gotSend)
			_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":7}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &Client{Token: "TEST:token", APIBase: srv.URL, HTTP: srv.Client()}
	me, err := GetMe(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if me.Username != "qingzhou_bot" {
		t.Fatalf("username = %q", me.Username)
	}
	if err := SendHTML(context.Background(), c, 42, "<b>hi</b>"); err != nil {
		t.Fatal(err)
	}
	if gotSend["chat_id"] != float64(42) || gotSend["parse_mode"] != "HTML" {
		t.Fatalf("send payload = %#v", gotSend)
	}
	if gotSend["disable_web_page_preview"] != true {
		t.Fatal("subscription URLs would be preview-fetched")
	}
}

func TestUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
	}))
	defer srv.Close()
	c := &Client{Token: "bad", APIBase: srv.URL, HTTP: srv.Client()}
	_, err := GetMe(context.Background(), c)
	ae, ok := err.(*APIError)
	if !ok || !ae.Unauthorized() {
		t.Fatalf("err = %v (%T)", err, err)
	}
}
