package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

type ctxKey int

const ctxKeyUserID ctxKey = iota

func (a *App) handleMe() http.HandlerFunc {
	type resp struct {
		UserID          string `json:"user_id"`
		DiscogsUserID   int64  `json:"discogs_user_id"`
		DiscogsUsername string `json:"discogs_username"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := a.requireSession(r)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, err)
			return
		}
		if a.db == nil {
			writeJSONError(w, http.StatusInternalServerError, errors.New("DATABASE_URL not configured"))
			return
		}

		var out resp
		out.UserID = userID
		err = a.db.QueryRow(r.Context(), `
select discogs_user_id, discogs_username
from users
where id = $1
`, userID).Scan(&out.DiscogsUserID, &out.DiscogsUsername)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}

		writeJSON(w, http.StatusOK, out)
	}
}

func (a *App) handleAlbums() http.HandlerFunc {
	type tag struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	type album struct {
		ID               string     `json:"id"`
		DiscogsReleaseID int64      `json:"discogs_release_id"`
		Title            string     `json:"title"`
		Artist           string     `json:"artist"`
		RecordLabel      *string    `json:"record_label,omitempty"`
		Year             *int       `json:"year,omitempty"`
		ThumbURL         *string    `json:"thumb_url,omitempty"`
		ResourceURL      *string    `json:"resource_url,omitempty"`
		LastSyncedAt     *time.Time `json:"last_synced_at,omitempty"`
		SpinCount        int        `json:"spin_count"`
		LastSpunAt       *time.Time `json:"last_spun_at,omitempty"`
		Tags             []tag      `json:"tags"`
	}

	type albumQuery struct {
		Q      string
		Artist string
		TagIDs []string
		Sort   string
		Order  string
	}

	parseAlbumQuery := func(v url.Values) albumQuery {
		q := albumQuery{
			Q:      strings.TrimSpace(v.Get("q")),
			Artist: strings.TrimSpace(v.Get("artist")),
			Sort:   strings.TrimSpace(v.Get("sort")),
			Order:  strings.ToLower(strings.TrimSpace(v.Get("order"))),
		}
		if q.Order != "desc" {
			q.Order = "asc"
		}
		if q.Sort == "" {
			q.Sort = "artist"
		}
		if raw := strings.TrimSpace(v.Get("tag_ids")); raw != "" {
			for _, part := range strings.Split(raw, ",") {
				part = strings.TrimSpace(part)
				if part != "" {
					q.TagIDs = append(q.TagIDs, part)
				}
			}
		}
		return q
	}

	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := a.requireSession(r)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, err)
			return
		}
		if a.db == nil {
			writeJSONError(w, http.StatusInternalServerError, errors.New("DATABASE_URL not configured"))
			return
		}

		// Optional: trigger a sync if requested.
		if r.URL.Query().Get("sync") == "1" {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
			defer cancel()
			if err := a.syncDiscogsCollection(ctx, userID); err != nil {
				writeJSONError(w, http.StatusBadGateway, err)
				return
			}
		}

		aq := parseAlbumQuery(r.URL.Query())

		var (
			args     []any
			argN     = 1
			whereSQL = "where a.user_id = $1"
		)
		args = append(args, userID)
		argN++

		if aq.Q != "" {
			whereSQL += " and (a.title ilike $" + strconv.Itoa(argN) + " or a.artist ilike $" + strconv.Itoa(argN) + ")"
			args = append(args, "%"+aq.Q+"%")
			argN++
		}
		if aq.Artist != "" {
			whereSQL += " and a.artist = $" + strconv.Itoa(argN)
			args = append(args, aq.Artist)
			argN++
		}
		if len(aq.TagIDs) > 0 {
			whereSQL += " and exists (select 1 from album_tags at where at.user_id = a.user_id and at.album_id = a.id and at.tag_id = any($" + strconv.Itoa(argN) + "::uuid[]))"
			args = append(args, aq.TagIDs)
			argN++
		}

		orderCol := "a.artist"
		switch aq.Sort {
		case "title":
			orderCol = "a.title"
		case "artist":
			orderCol = "a.artist"
		case "spin_count":
			orderCol = "spin_count"
		case "last_spun_at":
			orderCol = "last_spun_at"
		}
		orderDir := "asc"
		if aq.Order == "desc" {
			orderDir = "desc"
		}

		rows, err := a.db.Query(r.Context(), `
select
  a.id,
  a.discogs_release_id,
  a.title,
  a.artist,
  nullif(a.record_label, '') as record_label,
  a.year,
  a.thumb_url,
  a.resource_url,
  a.last_synced_at,
  count(s.id) as spin_count,
  max(s.spun_at) as last_spun_at
from albums a
left join spins s on s.album_id = a.id and s.user_id = a.user_id
`+whereSQL+`
group by a.id
order by `+orderCol+` `+orderDir+`, a.artist asc, a.title asc
limit 500
`, args...)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		defer rows.Close()

		var out []album
		var albumIDs []string
		for rows.Next() {
			var a album
			if err := rows.Scan(&a.ID, &a.DiscogsReleaseID, &a.Title, &a.Artist, &a.RecordLabel, &a.Year, &a.ThumbURL, &a.ResourceURL, &a.LastSyncedAt, &a.SpinCount, &a.LastSpunAt); err != nil {
				writeJSONError(w, http.StatusInternalServerError, err)
				return
			}
			if a.ResourceURL != nil {
				u := discogsWebReleaseURL(a.DiscogsReleaseID, *a.ResourceURL)
				a.ResourceURL = &u
			} else if a.DiscogsReleaseID > 0 {
				u := discogsWebReleaseURL(a.DiscogsReleaseID, "")
				if u != "" {
					a.ResourceURL = &u
				}
			}
			a.Tags = []tag{}
			out = append(out, a)
			albumIDs = append(albumIDs, a.ID)
		}
		if rows.Err() != nil {
			writeJSONError(w, http.StatusInternalServerError, rows.Err())
			return
		}

		// Attach tags in a second query (avoids complex aggregation).
		if len(albumIDs) > 0 {
			lrows, err := a.db.Query(r.Context(), `
select at.album_id, t.id, t.name
from album_tags at
join tags t on t.id = at.tag_id and t.user_id = at.user_id
where at.user_id = $1 and at.album_id = any($2::uuid[])
order by t.name asc
`, userID, albumIDs)
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, err)
				return
			}
			defer lrows.Close()

			byAlbum := make(map[string][]tag, len(albumIDs))
			for lrows.Next() {
				var albumID, tagID, name string
				if err := lrows.Scan(&albumID, &tagID, &name); err != nil {
					writeJSONError(w, http.StatusInternalServerError, err)
					return
				}
				byAlbum[albumID] = append(byAlbum[albumID], tag{ID: tagID, Name: name})
			}
			if lrows.Err() != nil {
				writeJSONError(w, http.StatusInternalServerError, lrows.Err())
				return
			}
			for i := range out {
				out[i].Tags = byAlbum[out[i].ID]
			}
		}

		writeJSON(w, http.StatusOK, out)
	}
}

func discogsWebReleaseURL(releaseID int64, stored string) string {
	// If we've already stored a human-facing Discogs URL, keep it.
	if s := strings.TrimSpace(stored); s != "" {
		if strings.Contains(s, "www.discogs.com/release/") || strings.Contains(s, "discogs.com/release/") {
			return s
		}
		// Legacy stored value: API resource URL like "https://api.discogs.com/releases/123"
		if strings.Contains(s, "api.discogs.com/releases/") && releaseID > 0 {
			return fmt.Sprintf("https://www.discogs.com/release/%d", releaseID)
		}
	}
	if releaseID > 0 {
		return fmt.Sprintf("https://www.discogs.com/release/%d", releaseID)
	}
	return ""
}

func (a *App) handleTags() http.HandlerFunc {
	type tag struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		AlbumCount int    `json:"album_count"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := a.requireSession(r)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, err)
			return
		}
		if a.db == nil {
			writeJSONError(w, http.StatusInternalServerError, errors.New("DATABASE_URL not configured"))
			return
		}
		rows, err := a.db.Query(r.Context(), `
select t.id, t.name, count(at.album_id) as album_count
from tags t
left join album_tags at on at.tag_id = t.id and at.user_id = t.user_id
where t.user_id = $1
group by t.id
order by t.name asc
`, userID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		defer rows.Close()
		var out []tag
		for rows.Next() {
			var x tag
			if err := rows.Scan(&x.ID, &x.Name, &x.AlbumCount); err != nil {
				writeJSONError(w, http.StatusInternalServerError, err)
				return
			}
			out = append(out, x)
		}
		if rows.Err() != nil {
			writeJSONError(w, http.StatusInternalServerError, rows.Err())
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (a *App) handleCreateTag() http.HandlerFunc {
	type req struct {
		Name string `json:"name"`
	}
	type resp struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := a.requireSession(r)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, err)
			return
		}
		if a.db == nil {
			writeJSONError(w, http.StatusInternalServerError, errors.New("DATABASE_URL not configured"))
			return
		}
		var in req
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSONError(w, http.StatusBadRequest, errors.New("invalid json"))
			return
		}
		name := strings.TrimSpace(in.Name)
		if name == "" {
			writeJSONError(w, http.StatusBadRequest, errors.New("name is required"))
			return
		}
		var id string
		// Upsert by (user_id, name) unique index.
		err = a.db.QueryRow(r.Context(), `
insert into tags (user_id, name)
values ($1, $2)
on conflict (user_id, name) do update
set name = excluded.name,
    updated_at = now()
returning id
`, userID, name).Scan(&id)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusCreated, resp{ID: id, Name: name})
	}
}

func (a *App) handleAddAlbumTag() http.HandlerFunc {
	type req struct {
		TagID string `json:"tag_id,omitempty"`
		Name  string `json:"name,omitempty"` // optional: create/find tag by name
	}
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := a.requireSession(r)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, err)
			return
		}
		if a.db == nil {
			writeJSONError(w, http.StatusInternalServerError, errors.New("DATABASE_URL not configured"))
			return
		}
		albumID := strings.TrimSpace(chi.URLParam(r, "albumID"))
		if albumID == "" {
			writeJSONError(w, http.StatusBadRequest, errors.New("albumID required"))
			return
		}

		// Ensure album belongs to user.
		var ok bool
		if err := a.db.QueryRow(r.Context(), `select exists(select 1 from albums where id=$1 and user_id=$2)`, albumID, userID).Scan(&ok); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		if !ok {
			writeJSONError(w, http.StatusNotFound, errors.New("album not found"))
			return
		}

		var in req
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSONError(w, http.StatusBadRequest, errors.New("invalid json"))
			return
		}

		tagID := strings.TrimSpace(in.TagID)
		if tagID == "" {
			name := strings.TrimSpace(in.Name)
			if name == "" {
				writeJSONError(w, http.StatusBadRequest, errors.New("tag_id or name required"))
				return
			}
			if err := a.db.QueryRow(r.Context(), `
insert into tags (user_id, name)
values ($1, $2)
on conflict (user_id, name) do update
set name = excluded.name,
    updated_at = now()
returning id
`, userID, name).Scan(&tagID); err != nil {
				writeJSONError(w, http.StatusInternalServerError, err)
				return
			}
		} else {
			// Ensure tag belongs to user.
			var exists bool
			if err := a.db.QueryRow(r.Context(), `select exists(select 1 from tags where id=$1 and user_id=$2)`, tagID, userID).Scan(&exists); err != nil {
				writeJSONError(w, http.StatusInternalServerError, err)
				return
			}
			if !exists {
				writeJSONError(w, http.StatusNotFound, errors.New("tag not found"))
				return
			}
		}

		_, err = a.db.Exec(r.Context(), `
insert into album_tags (user_id, album_id, tag_id)
values ($1, $2, $3)
on conflict (album_id, tag_id) do nothing
`, userID, albumID, tagID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *App) handleRemoveAlbumTag() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := a.requireSession(r)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, err)
			return
		}
		if a.db == nil {
			writeJSONError(w, http.StatusInternalServerError, errors.New("DATABASE_URL not configured"))
			return
		}
		albumID := strings.TrimSpace(chi.URLParam(r, "albumID"))
		tagID := strings.TrimSpace(chi.URLParam(r, "tagID"))
		if albumID == "" || tagID == "" {
			writeJSONError(w, http.StatusBadRequest, errors.New("albumID and tagID required"))
			return
		}
		ct, err := a.db.Exec(r.Context(), `
delete from album_tags
where user_id = $1 and album_id = $2 and tag_id = $3
`, userID, albumID, tagID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		if ct.RowsAffected() == 0 {
			writeJSONError(w, http.StatusNotFound, errors.New("album tag not found"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *App) handleSpins() http.HandlerFunc {
	type spin struct {
		ID          string    `json:"id"`
		AlbumID     string    `json:"album_id"`
		SpunAt      time.Time `json:"spun_at"`
		Note        *string   `json:"note,omitempty"`
		AlbumTitle  string    `json:"album_title"`
		AlbumArtist string    `json:"album_artist"`
		AlbumThumb  *string   `json:"album_thumb_url,omitempty"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := a.requireSession(r)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, err)
			return
		}
		if a.db == nil {
			writeJSONError(w, http.StatusInternalServerError, errors.New("DATABASE_URL not configured"))
			return
		}

		rows, err := a.db.Query(r.Context(), `
select
  s.id,
  s.album_id,
  s.spun_at,
  nullif(s.note, '') as note,
  a.title as album_title,
  a.artist as album_artist,
  a.thumb_url as album_thumb_url
from spins s
join albums a on a.id = s.album_id and a.user_id = s.user_id
where s.user_id = $1
order by s.spun_at desc
limit 200
`, userID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		defer rows.Close()

		var out []spin
		for rows.Next() {
			var s spin
			if err := rows.Scan(&s.ID, &s.AlbumID, &s.SpunAt, &s.Note, &s.AlbumTitle, &s.AlbumArtist, &s.AlbumThumb); err != nil {
				writeJSONError(w, http.StatusInternalServerError, err)
				return
			}
			out = append(out, s)
		}
		if rows.Err() != nil {
			writeJSONError(w, http.StatusInternalServerError, rows.Err())
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (a *App) handleCreateSpin() http.HandlerFunc {
	type req struct {
		AlbumID string  `json:"album_id"`
		SpunAt  *string `json:"spun_at,omitempty"` // RFC3339
		Note    *string `json:"note,omitempty"`
	}
	type resp struct {
		ID string `json:"id"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := a.requireSession(r)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, err)
			return
		}
		if a.db == nil {
			writeJSONError(w, http.StatusInternalServerError, errors.New("DATABASE_URL not configured"))
			return
		}

		var in req
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSONError(w, http.StatusBadRequest, errors.New("invalid json"))
			return
		}
		in.AlbumID = strings.TrimSpace(in.AlbumID)
		if in.AlbumID == "" {
			writeJSONError(w, http.StatusBadRequest, errors.New("album_id is required"))
			return
		}

		spunAt := time.Now()
		if in.SpunAt != nil && strings.TrimSpace(*in.SpunAt) != "" {
			t, err := time.Parse(time.RFC3339, strings.TrimSpace(*in.SpunAt))
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, errors.New("spun_at must be RFC3339"))
				return
			}
			spunAt = t
		}

		var note string
		if in.Note != nil {
			note = strings.TrimSpace(*in.Note)
		}

		// Ensure album belongs to user.
		var exists bool
		if err := a.db.QueryRow(r.Context(), `select exists(select 1 from albums where id=$1 and user_id=$2)`, in.AlbumID, userID).Scan(&exists); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		if !exists {
			writeJSONError(w, http.StatusBadRequest, errors.New("unknown album_id"))
			return
		}

		var id string
		err = a.db.QueryRow(r.Context(), `
insert into spins (user_id, album_id, spun_at, note)
values ($1, $2, $3, nullif($4, ''))
returning id
`, userID, in.AlbumID, spunAt, note).Scan(&id)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusCreated, resp{ID: id})
	}
}

func (a *App) handleDeleteSpin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := a.requireSession(r)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, err)
			return
		}
		if a.db == nil {
			writeJSONError(w, http.StatusInternalServerError, errors.New("DATABASE_URL not configured"))
			return
		}

		spinID := chi.URLParam(r, "spinID")
		spinID = strings.TrimSpace(spinID)
		if spinID == "" {
			writeJSONError(w, http.StatusBadRequest, errors.New("spinID required"))
			return
		}

		ct, err := a.db.Exec(r.Context(), `delete from spins where id=$1 and user_id=$2`, spinID, userID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		if ct.RowsAffected() == 0 {
			writeJSONError(w, http.StatusNotFound, errors.New("spin not found"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *App) handleAlbumsSync() http.HandlerFunc {
	type resp struct {
		Status string `json:"status"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := a.requireSession(r)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, err)
			return
		}
		if a.db == nil {
			writeJSONError(w, http.StatusInternalServerError, errors.New("DATABASE_URL not configured"))
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		if err := a.syncDiscogsCollection(ctx, userID); err != nil {
			writeJSONError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, resp{Status: "ok"})
	}
}

func (a *App) requireSession(r *http.Request) (string, error) {
	sealer, err := newSealerFromEnv()
	if err != nil {
		return "", err
	}
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", errors.New("not authenticated")
	}
	b, err := sealer.openFromString(c.Value)
	if err != nil {
		return "", errors.New("invalid session")
	}
	userID := string(b)
	if userID == "" {
		return "", errors.New("invalid session")
	}
	return userID, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
