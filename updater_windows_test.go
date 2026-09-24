package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"1.0.1", "1.0.0", true},
		{"1.10.0", "1.9.9", true},
		{"2.0", "1.99.99", true},
		{"1.0.0", "1.0.0", false},
		{"1.0", "1.0.0", false},
		{"0.9.9", "1.0.0", false},
	}
	for _, c := range cases {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestCheckUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v1.2.0","html_url":"x","assets":[
			{"name":"AudioManagerPro.exe","browser_download_url":"https://e/exe"},
			{"name":"AudioManagerPro.exe.sha256","browser_download_url":"https://e/sum"}]}`)
	}))
	defer srv.Close()
	latestAPI = srv.URL
	defer func() { latestAPI = defaultAPI; version = "dev" }()

	version = "dev"
	if info, _ := checkUpdate(); info != nil {
		t.Fatal("dev builds must not offer updates")
	}
	version = "1.1.9"
	info, err := checkUpdate()
	if err != nil || info == nil || info.Version != "1.2.0" || info.exeURL != "https://e/exe" || info.sumURL != "https://e/sum" {
		t.Fatalf("got %+v, %v", info, err)
	}
	version = "1.2.0"
	if info, _ := checkUpdate(); info != nil {
		t.Fatal("same version must not be offered")
	}
}
