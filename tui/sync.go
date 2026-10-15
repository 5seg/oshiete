package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Syncer pulls new events from the collector into the local mirror.
type Syncer struct {
	cfg Config
	hc  *http.Client
}

func NewSyncer(cfg Config) *Syncer {
	return &Syncer{cfg: cfg, hc: &http.Client{Timeout: 30 * time.Second}}
}

// Event mirrors one row of the collector's `events` table.
type Event struct {
	ID      int64   `json:"id"`
	Ts      int64   `json:"ts"`
	Site    string  `json:"site"`
	Path    string  `json:"path"`
	Ref     *string `json:"ref"`
	Country *string `json:"country"`
	Device  *string `json:"device"`
	Browser *string `json:"browser"`
	OS      *string `json:"os"`
	Width   *int    `json:"width"`
	Lang    *string `json:"lang"`
	Visitor string  `json:"visitor"`
	Session string  `json:"session"`
	IsBot   int     `json:"is_bot"`
}

type siteInfo struct {
	Site     string `json:"site"`
	Events   int    `json:"events"`
	Cursor   int64  `json:"cursor"`
	LastSeen int64  `json:"last_seen"`
}

type sinceResponse struct {
	Events  []Event `json:"events"`
	Cursor  int64   `json:"cursor"`
	HasMore bool    `json:"has_more"`
}

const pageSize = 5000

func (s *Syncer) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.Endpoint+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.Token)
	req.Header.Set("Accept", "application/json")

	resp, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("reach %s: %w", s.cfg.Endpoint, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return fmt.Errorf("sync token rejected by %s", s.cfg.Endpoint)
	default:
		return fmt.Errorf("%s returned %s", path, resp.Status)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// Discover lists the sites the collector knows about, newest activity first.
func (s *Syncer) Discover(ctx context.Context) ([]siteInfo, error) {
	var out struct {
		Sites []siteInfo `json:"sites"`
	}
	if err := s.get(ctx, "/sites", &out); err != nil {
		return nil, err
	}
	return out.Sites, nil
}

// Run pulls every site. httpErr is non-nil if the network failed, but whatever
// was fetched before the failure is already committed to the mirror.
func (s *Syncer) Run(ctx context.Context, store *Store) (added int, err error) {
	sites, err := s.Discover(ctx)
	if err != nil {
		return 0, err
	}
	for _, si := range sites {
		n, err := s.pull(ctx, store, si.Site)
		added += n
		if err != nil {
			return added, err
		}
	}
	return added, nil
}

func (s *Syncer) pull(ctx context.Context, store *Store, site string) (int, error) {
	cursor, err := store.Cursor(site)
	if err != nil {
		return 0, err
	}

	added := 0
	for {
		q := url.Values{}
		q.Set("site", site)
		q.Set("id", fmt.Sprintf("%d", cursor))
		q.Set("limit", fmt.Sprintf("%d", pageSize))

		var page sinceResponse
		if err := s.get(ctx, "/since?"+q.Encode(), &page); err != nil {
			return added, err
		}
		if len(page.Events) == 0 {
			break
		}

		n, err := store.InsertEvents(page.Events)
		if err != nil {
			return added, err
		}
		added += n

		cursor = page.Cursor
		if err := store.SetCursor(site, cursor); err != nil {
			return added, err
		}
		if !page.HasMore {
			break
		}
	}
	return added, nil
}
