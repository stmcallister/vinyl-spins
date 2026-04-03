package app

import (
	"errors"
	"net/http"
	"time"
)

func (a *App) handleReports() http.HandlerFunc {
	type spinOverTime struct {
		Period    string `json:"period"`
		SpinCount int    `json:"spin_count"`
	}
	type topArtist struct {
		Artist      string `json:"artist"`
		SpinCount   int    `json:"spin_count"`
		RecordCount int    `json:"record_count"`
	}
	type reportRecord struct {
		ID         string     `json:"id"`
		Title      string     `json:"title"`
		Artist     string     `json:"artist"`
		Year       *int       `json:"year,omitempty"`
		ThumbURL   *string    `json:"thumb_url,omitempty"`
		LastSpunAt *time.Time `json:"last_spun_at,omitempty"`
		SpinCount  int        `json:"spin_count"`
	}
	type collectionStats struct {
		TotalRecords   int     `json:"total_records"`
		PlayedRecords  int     `json:"played_records"`
		NeverPlayed    int     `json:"never_played"`
		UtilizationPct float64 `json:"utilization_pct"`
	}
	type response struct {
		SpinsOverTime []spinOverTime  `json:"spins_over_time"`
		TopArtists    []topArtist     `json:"top_artists"`
		NeverPlayed   []reportRecord  `json:"never_played"`
		Neglected     []reportRecord  `json:"neglected"`
		Stats         collectionStats `json:"stats"`
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

		period := r.URL.Query().Get("period")
		if period != "month" {
			period = "week"
		}
		lookback := "52 weeks"
		if period == "month" {
			lookback = "24 months"
		}

		out := response{
			SpinsOverTime: []spinOverTime{},
			TopArtists:    []topArtist{},
			NeverPlayed:   []reportRecord{},
			Neglected:     []reportRecord{},
		}

		// ── Spins over time ──────────────────────────────────────────────────
		rows, err := a.db.Query(r.Context(), `
select
  date_trunc($1, spun_at)::date as period,
  count(*)::int as spin_count
from spins
where user_id = $2
  and spun_at >= now() - $3::interval
group by period
order by period
`, period, userID, lookback)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		for rows.Next() {
			var s spinOverTime
			var t time.Time
			if err := rows.Scan(&t, &s.SpinCount); err != nil {
				rows.Close()
				writeJSONError(w, http.StatusInternalServerError, err)
				return
			}
			s.Period = t.Format("2006-01-02")
			out.SpinsOverTime = append(out.SpinsOverTime, s)
		}
		rows.Close()

		// ── Top artists ──────────────────────────────────────────────────────
		rows, err = a.db.Query(r.Context(), `
select
  a.artist,
  count(s.id)::int as spin_count,
  count(distinct a.id)::int as record_count
from albums a
left join spins s on s.album_id = a.id and s.user_id = $1
where a.user_id = $1
group by a.artist
order by spin_count desc, a.artist
limit 20
`, userID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		for rows.Next() {
			var ta topArtist
			if err := rows.Scan(&ta.Artist, &ta.SpinCount, &ta.RecordCount); err != nil {
				rows.Close()
				writeJSONError(w, http.StatusInternalServerError, err)
				return
			}
			out.TopArtists = append(out.TopArtists, ta)
		}
		rows.Close()

		// ── Never played ─────────────────────────────────────────────────────
		rows, err = a.db.Query(r.Context(), `
select a.id, a.title, a.artist, a.year, a.thumb_url
from albums a
where a.user_id = $1
  and not exists (
    select 1 from spins s where s.album_id = a.id and s.user_id = $1
  )
order by a.artist, a.title
`, userID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		for rows.Next() {
			var rr reportRecord
			if err := rows.Scan(&rr.ID, &rr.Title, &rr.Artist, &rr.Year, &rr.ThumbURL); err != nil {
				rows.Close()
				writeJSONError(w, http.StatusInternalServerError, err)
				return
			}
			out.NeverPlayed = append(out.NeverPlayed, rr)
		}
		rows.Close()

		// ── Neglected (played but not in 6 months) ───────────────────────────
		rows, err = a.db.Query(r.Context(), `
select
  a.id, a.title, a.artist, a.year, a.thumb_url,
  max(s.spun_at) as last_spun_at,
  count(s.id)::int as spin_count
from albums a
join spins s on s.album_id = a.id and s.user_id = $1
where a.user_id = $1
group by a.id, a.title, a.artist, a.year, a.thumb_url
having max(s.spun_at) < now() - interval '6 months'
order by last_spun_at asc
`, userID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		for rows.Next() {
			var rr reportRecord
			if err := rows.Scan(&rr.ID, &rr.Title, &rr.Artist, &rr.Year, &rr.ThumbURL, &rr.LastSpunAt, &rr.SpinCount); err != nil {
				rows.Close()
				writeJSONError(w, http.StatusInternalServerError, err)
				return
			}
			out.Neglected = append(out.Neglected, rr)
		}
		rows.Close()

		// ── Collection stats ─────────────────────────────────────────────────
		err = a.db.QueryRow(r.Context(), `
select
  count(distinct a.id)::int,
  count(distinct case when s.id is not null then a.id end)::int
from albums a
left join spins s on s.album_id = a.id and s.user_id = $1
where a.user_id = $1
`, userID).Scan(&out.Stats.TotalRecords, &out.Stats.PlayedRecords)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err)
			return
		}
		out.Stats.NeverPlayed = out.Stats.TotalRecords - out.Stats.PlayedRecords
		if out.Stats.TotalRecords > 0 {
			out.Stats.UtilizationPct = float64(out.Stats.PlayedRecords) / float64(out.Stats.TotalRecords) * 100
		}

		writeJSON(w, http.StatusOK, out)
	}
}
