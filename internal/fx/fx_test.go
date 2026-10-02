package fx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchFallsBackToTheNextSource(t *testing.T) {
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer broken.Close()
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result":"success","rates":{"EUR":0.9}}`))
	}))
	defer empty.Close()
	working := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"amount":1.0,"base":"USD","rates":{"CNY":7.1234}}`))
	}))
	defer working.Close()

	saved := Sources
	defer func() { Sources = saved }()

	Sources = []string{broken.URL, empty.URL, working.URL}
	rate, err := Fetch(context.Background(), http.DefaultClient)
	if err != nil || rate != 7.1234 {
		t.Fatalf("Fetch = %v, %v", rate, err)
	}

	Sources = []string{broken.URL, empty.URL}
	if _, err := Fetch(context.Background(), http.DefaultClient); err == nil {
		t.Fatal("Fetch succeeded without a usable source")
	}
}
