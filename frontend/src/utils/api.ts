// In production we typically run UI and API on the same origin behind an Ingress.
// In that case, leaving VITE_API_URL unset makes requests same-origin.
const API_URL = (import.meta.env.VITE_API_URL as string | undefined) ?? (import.meta.env.PROD ? "" : "http://localhost:8080");

function qs(params: Record<string, string | undefined>): string {
  const u = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v && v.trim()) u.set(k, v);
  }
  const s = u.toString();
  return s ? `?${s}` : "";
}

async function fetchJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${API_URL}${path}`, {
    credentials: "include",
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers || {}),
    },
  });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new Error(`${path} failed: ${res.status}${text ? `: ${text}` : ""}`);
  }
  // 204: no content
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

type ImportOggerPlaylogResponse = {
  total_rows: number;
  parsed_rows: number;
  deduped_rows: number;
  matched_rows: number;
  inserted_spins: number;
  already_existed: number;
  unmatched_rows: number;
  unmatched_release_ids?: number[];
  parse_errors: number;
  timezone: string;
};

export const api = {
  async healthz(): Promise<string> {
    const res = await fetch(`${API_URL}/healthz`);
    if (!res.ok) throw new Error(`healthz failed: ${res.status}`);
    return await res.text();
  },

  async me(): Promise<{ user_id: string; discogs_user_id: number; discogs_username: string }> {
    return await fetchJSON("/api/me");
  },

  async records(input?: {
    q?: string;
    artist?: string;
    tag_ids?: string; // comma-separated
    sort?: "artist" | "title" | "spin_count" | "last_spun_at";
    order?: "asc" | "desc";
  }): Promise<
    Array<{
      id: string;
      discogs_release_id: number;
      title: string;
      artist: string;
      record_label?: string;
      year?: number;
      thumb_url?: string;
      resource_url?: string;
      last_synced_at?: string;
      spin_count: number;
      last_spun_at?: string;
      tags: Array<{ id: string; name: string }>;
    }>
  > {
    return await fetchJSON(
      `/api/records${qs({
        q: input?.q,
        artist: input?.artist,
        tag_ids: input?.tag_ids,
        sort: input?.sort,
        order: input?.order,
      })}`,
    );
  },

  async pickRecord(input?: { q?: string; artist?: string; tag_ids?: string }): Promise<{
    id: string;
    discogs_release_id: number;
    title: string;
    artist: string;
    year?: number;
    thumb_url?: string;
    resource_url?: string;
    spin_count: number;
    last_spun_at?: string;
  }> {
    return await fetchJSON(
      `/api/records/pick${qs({
        q: input?.q,
        artist: input?.artist,
        tag_ids: input?.tag_ids,
      })}`,
    );
  },

  async recordDetail(recordID: string): Promise<{
    id: string;
    discogs_release_id: number;
    title: string;
    artist: string;
    year?: number;
    thumb_url?: string;
    resource_url?: string;
    last_synced_at?: string;
    spin_count: number;
    last_spun_at?: string;
    tags: Array<{ id: string; name: string }>;
    spins: Array<{ id: string; spun_at: string; note?: string }>;
    discogs?: {
      release_id: number;
      title: string;
      year: number;
      released?: string;
      master_id?: number;
      original_year?: number;
      country?: string;
      formats: Array<{ name: string; qty: string; text: string; descriptions: string[] }>;
      genres?: string[];
      styles?: string[];
      notes?: string;
    };
  }> {
    return await fetchJSON(`/api/records/${encodeURIComponent(recordID)}`);
  },

  async syncRecords(): Promise<{ status: string }> {
    return await fetchJSON("/api/records/sync", { method: "POST", body: "{}" });
  },

  async tags(): Promise<Array<{ id: string; name: string; record_count: number }>> {
    return await fetchJSON("/api/tags");
  },

  async createTag(input: { name: string }): Promise<{ id: string; name: string }> {
    return await fetchJSON("/api/tags", { method: "POST", body: JSON.stringify(input) });
  },

  async updateTag(tagID: string, input: { name: string }): Promise<{ id: string; name: string }> {
    return await fetchJSON(`/api/tags/${encodeURIComponent(tagID)}`, { method: "PUT", body: JSON.stringify(input) });
  },

  async deleteTag(tagID: string): Promise<void> {
    await fetchJSON(`/api/tags/${encodeURIComponent(tagID)}`, { method: "DELETE" });
  },

  async addRecordTag(recordID: string, input: { tag_id?: string; name?: string }): Promise<void> {
    await fetchJSON(`/api/records/${encodeURIComponent(recordID)}/tags`, {
      method: "POST",
      body: JSON.stringify(input),
    });
  },

  async removeRecordTag(recordID: string, tagID: string): Promise<void> {
    await fetchJSON(`/api/records/${encodeURIComponent(recordID)}/tags/${encodeURIComponent(tagID)}`, {
      method: "DELETE",
    });
  },

  // Backwards-compatible aliases (historically called these "labels").
  async labels(): Promise<Array<{ id: string; name: string; record_count: number }>> {
    return await fetchJSON("/api/tags");
  },

  async createLabel(input: { name: string }): Promise<{ id: string; name: string }> {
    return await fetchJSON("/api/tags", { method: "POST", body: JSON.stringify(input) });
  },

  async addRecordLabel(recordID: string, input: { label_id?: string; name?: string }): Promise<void> {
    await fetchJSON(`/api/records/${encodeURIComponent(recordID)}/tags`, {
      method: "POST",
      body: JSON.stringify({ tag_id: input.label_id, name: input.name }),
    });
  },

  async removeRecordLabel(recordID: string, labelID: string): Promise<void> {
    await fetchJSON(`/api/records/${encodeURIComponent(recordID)}/tags/${encodeURIComponent(labelID)}`, {
      method: "DELETE",
    });
  },

  async spins(): Promise<
    Array<{
      id: string;
      record_id: string;
      spun_at: string;
      note?: string;
      record_title: string;
      record_artist: string;
      record_thumb_url?: string;
    }>
  > {
    return await fetchJSON("/api/spins");
  },

  async createSpin(input: { record_id: string; spun_at?: string; note?: string }): Promise<{ id: string }> {
    return await fetchJSON("/api/spins", { method: "POST", body: JSON.stringify(input) });
  },

  async deleteSpin(spinID: string): Promise<void> {
    await fetchJSON(`/api/spins/${encodeURIComponent(spinID)}`, { method: "DELETE" });
  },

  async logout(): Promise<void> {
    await fetchJSON("/auth/logout", { method: "POST", body: "{}" });
  },

  async importOggerPlaylog(file: File, input?: { tz?: string }): Promise<ImportOggerPlaylogResponse> {
    const fd = new FormData();
    fd.set("file", file);
    const tz = input?.tz?.trim();
    const qs = tz ? `?tz=${encodeURIComponent(tz)}` : "";
    const res = await fetch(`${API_URL}/api/import/ogger-playlog${qs}`, {
      method: "POST",
      credentials: "include",
      body: fd,
    });
    if (!res.ok) {
      const text = await res.text().catch(() => "");
      throw new Error(`/api/import/ogger-playlog failed: ${res.status}${text ? `: ${text}` : ""}`);
    }
    return (await res.json()) as ImportOggerPlaylogResponse;
  },
};
