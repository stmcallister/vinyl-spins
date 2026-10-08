package app

import (
	"context"
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// backupMu serializes writing, pruning, and purging backup files so a purge
// can't miss a file that an export is in the middle of writing.
var backupMu sync.Mutex

// StartDailyExport launches a background goroutine that writes a CSV backup of
// all spins once per day. It runs immediately on startup (so missed days are
// covered), then waits until the next UTC midnight for subsequent runs.
//
// Files are written to dir as spins-YYYY-MM-DD.csv and the 30 most-recent
// files are kept; older ones are deleted automatically.
func StartDailyExport(ctx context.Context, db *pgxpool.Pool, dir string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("export: cannot create backup dir %s: %v", dir, err)
		return
	}
	go func() {
		// Run once right away, then daily at midnight UTC.
		if err := exportSpins(ctx, db, dir); err != nil {
			log.Printf("export: %v", err)
		}
		for {
			next := nextMidnightUTC()
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Until(next)):
			}
			if err := exportSpins(ctx, db, dir); err != nil {
				log.Printf("export: %v", err)
			}
		}
	}()
}

// nextMidnightUTC returns the next UTC midnight (00:00:00).
func nextMidnightUTC() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
}

// exportSpins writes a dated CSV to dir and prunes files older than 30 days.
func exportSpins(ctx context.Context, db *pgxpool.Pool, dir string) error {
	backupMu.Lock()
	defer backupMu.Unlock()

	date := time.Now().UTC().Format("2006-01-02")
	filename := filepath.Join(dir, fmt.Sprintf("spins-%s.csv", date))

	rows, err := db.Query(ctx, `
select
  s.id,
  s.user_id,
  u.discogs_username,
  a.discogs_release_id,
  a.title   as album_title,
  a.artist  as album_artist,
  s.spun_at,
  coalesce(nullif(s.note, ''), '') as note
from spins s
join albums a on a.id = s.album_id
join users  u on u.id = s.user_id
order by s.spun_at asc
`)
	if err != nil {
		return fmt.Errorf("query spins: %w", err)
	}
	defer rows.Close()

	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("create file %s: %w", filename, err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	_ = w.Write([]string{"spin_id", "user_id", "discogs_username", "discogs_release_id", "record_title", "record_artist", "spun_at", "note"})

	var count int
	for rows.Next() {
		var (
			spinID           string
			userID           string
			discogsUsername  string
			discogsReleaseID int64
			recordTitle      string
			recordArtist     string
			spunAt           time.Time
			note             string
		)
		if err := rows.Scan(&spinID, &userID, &discogsUsername, &discogsReleaseID, &recordTitle, &recordArtist, &spunAt, &note); err != nil {
			return fmt.Errorf("scan row: %w", err)
		}
		_ = w.Write([]string{
			spinID,
			userID,
			discogsUsername,
			fmt.Sprintf("%d", discogsReleaseID),
			recordTitle,
			recordArtist,
			spunAt.UTC().Format(time.RFC3339),
			note,
		})
		count++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows: %w", err)
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return fmt.Errorf("csv flush: %w", err)
	}

	log.Printf("export: wrote %d spins to %s", count, filename)

	pruneOldBackups(dir, 30)
	return nil
}

// pruneOldBackups keeps the most-recent `keep` spins-*.csv files and deletes
// the rest.
func pruneOldBackups(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("export: readdir %s: %v", dir, err)
		return
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "spins-") && strings.HasSuffix(e.Name(), ".csv") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}

	sort.Strings(files) // lexicographic == chronological for YYYY-MM-DD names
	if len(files) <= keep {
		return
	}

	for _, old := range files[:len(files)-keep] {
		if err := os.Remove(old); err != nil {
			log.Printf("export: remove %s: %v", old, err)
		} else {
			log.Printf("export: pruned %s", old)
		}
	}
}

// PurgeUserFromBackups rewrites every spins-*.csv backup in dir without the
// rows belonging to userID. Called when a user deletes their account so their
// data doesn't linger in backups. Each file is rewritten via a temp file and
// rename so a crash never leaves a truncated backup.
func PurgeUserFromBackups(dir, userID string) error {
	backupMu.Lock()
	defer backupMu.Unlock()

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("readdir %s: %w", dir, err)
	}

	var errs []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "spins-") || !strings.HasSuffix(e.Name(), ".csv") {
			continue
		}
		if err := purgeUserFromFile(filepath.Join(dir, e.Name()), userID); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("purge backups: %s", strings.Join(errs, "; "))
	}
	return nil
}

func purgeUserFromFile(path, userID string) error {
	in, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	records, err := csv.NewReader(in).ReadAll()
	in.Close()
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if len(records) == 0 {
		return nil
	}

	userCol := -1
	for i, h := range records[0] {
		if h == "user_id" {
			userCol = i
			break
		}
	}
	if userCol < 0 {
		return fmt.Errorf("%s: no user_id column", path)
	}

	kept := records[:1]
	for _, rec := range records[1:] {
		if userCol < len(rec) && rec[userCol] == userID {
			continue
		}
		kept = append(kept, rec)
	}
	if len(kept) == len(records) {
		return nil
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".purge-*.csv")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	w := csv.NewWriter(tmp)
	if err := w.WriteAll(kept); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp for %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	log.Printf("export: purged %d rows for deleted user from %s", len(records)-len(kept), path)
	return nil
}
