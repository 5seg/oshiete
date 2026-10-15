package main

import (
	"context"
	"fmt"
	"time"
)

// InsertEvents writes a page into the mirror. It is idempotent: the sync cursor
// only advances after a commit, so a crash replays at worst.
func (s *Store) InsertEvents(events []Event) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO events
		  (id, ts, site, path, ref, country, device, browser, os, width, lang, visitor, session, is_bot)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	n := 0
	for _, e := range events {
		if _, err := stmt.Exec(
			e.ID, e.Ts, e.Site, e.Path, e.Ref, e.Country, e.Device, e.Browser,
			e.OS, e.Width, e.Lang, e.Visitor, e.Session, e.IsBot,
		); err != nil {
			return n, fmt.Errorf("insert event %d: %w", e.ID, err)
		}
		n++
	}
	return n, tx.Commit()
}

// ---- commands -------------------------------------------------------------

type syncedMsg struct {
	added int
	err   error
}

type tickMsg time.Time

func runSync(sy *Syncer, store *Store) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	n, err := sy.Run(ctx, store)
	if err != nil {
		return err
	}
	fmt.Printf("synced %d new event(s)\n", n)
	return nil
}
