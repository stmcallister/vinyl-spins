import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../utils/api";

export function App() {
  const qc = useQueryClient();
  const health = useQuery({
    queryKey: ["healthz"],
    queryFn: api.healthz,
    retry: false,
  });

  const apiUrl = import.meta.env.VITE_API_URL || "http://localhost:8080";

  const me = useQuery({
    queryKey: ["me"],
    queryFn: api.me,
    retry: false,
  });

  const tags = useQuery({
    queryKey: ["tags"],
    queryFn: api.tags,
    enabled: me.isSuccess,
  });

  const [search, setSearch] = useState("");
  const [artistFilter, setArtistFilter] = useState("");
  const [tagFilterIDs, setTagFilterIDs] = useState<string[]>([]);
  const [sort, setSort] = useState<"artist" | "title" | "spin_count" | "last_spun_at">("artist");
  const [order, setOrder] = useState<"asc" | "desc">("asc");

  const albums = useQuery({
    queryKey: ["albums", { search, artistFilter, tagFilterIDs, sort, order }],
    queryFn: () =>
      api.albums({
        q: search || undefined,
        artist: artistFilter || undefined,
        tag_ids: tagFilterIDs.length ? tagFilterIDs.join(",") : undefined,
        sort,
        order,
      }),
    enabled: me.isSuccess,
  });

  const spins = useQuery({
    queryKey: ["spins"],
    queryFn: api.spins,
    enabled: me.isSuccess,
  });

  const syncAlbums = useMutation({
    mutationFn: api.syncAlbums,
    onSuccess: async () => {
      await Promise.all([qc.invalidateQueries({ queryKey: ["albums"] })]);
    },
  });

  const createTag = useMutation({
    mutationFn: api.createTag,
    onSuccess: async () => {
      await Promise.all([qc.invalidateQueries({ queryKey: ["tags"] })]);
    },
  });

  const addAlbumTag = useMutation({
    mutationFn: async (input: { albumID: string; tag_id?: string; name?: string }) => {
      await api.addAlbumTag(input.albumID, { tag_id: input.tag_id, name: input.name });
    },
    onSuccess: async () => {
      await Promise.all([
        qc.invalidateQueries({ queryKey: ["albums"] }),
        qc.invalidateQueries({ queryKey: ["tags"] }),
      ]);
    },
  });

  const removeAlbumTag = useMutation({
    mutationFn: async (input: { albumID: string; tagID: string }) => {
      await api.removeAlbumTag(input.albumID, input.tagID);
    },
    onSuccess: async () => {
      await Promise.all([
        qc.invalidateQueries({ queryKey: ["albums"] }),
        qc.invalidateQueries({ queryKey: ["tags"] }),
      ]);
    },
  });

  const createSpin = useMutation({
    mutationFn: api.createSpin,
    onSuccess: async () => {
      await Promise.all([
        qc.invalidateQueries({ queryKey: ["spins"] }),
        qc.invalidateQueries({ queryKey: ["albums"] }),
      ]);
    },
  });

  const deleteSpin = useMutation({
    mutationFn: api.deleteSpin,
    onSuccess: async () => {
      await Promise.all([
        qc.invalidateQueries({ queryKey: ["spins"] }),
        qc.invalidateQueries({ queryKey: ["albums"] }),
      ]);
    },
  });

  const logout = useMutation({
    mutationFn: api.logout,
    onSuccess: async () => {
      await Promise.all([
        qc.invalidateQueries({ queryKey: ["me"] }),
        qc.invalidateQueries({ queryKey: ["albums"] }),
        qc.invalidateQueries({ queryKey: ["tags"] }),
        qc.invalidateQueries({ queryKey: ["spins"] }),
      ]);
    },
  });

  const [spunAtLocal, setSpunAtLocal] = useState("");
  const [note, setNote] = useState("");
  const [newTagName, setNewTagName] = useState("");
  const [selectedAlbumID, setSelectedAlbumID] = useState("");

  const albumOptions = useMemo(() => {
    if (!albums.data) return [];
    return albums.data.map((a) => ({
      id: a.id,
      label: `${a.artist} — ${a.title}${a.year ? ` (${a.year})` : ""}`,
    }));
  }, [albums.data]);

  const artistOptions = useMemo(() => {
    const set = new Set<string>();
    for (const a of albums.data ?? []) set.add(a.artist);
    return Array.from(set).sort((x, y) => x.localeCompare(y));
  }, [albums.data]);

  const tagOptions = useMemo(() => tags.data ?? [], [tags.data]);

  return (
    <div className="min-h-dvh">
      <header className="border-b border-zinc-800">
        <div className="mx-auto flex max-w-4xl items-center justify-between px-4 py-4">
          <div>
            <div className="text-lg font-semibold">Vinyl Spin Tracker</div>
            <div className="text-sm text-zinc-400">
              {me.isSuccess
                ? `Connected as ${me.data.discogs_username}`
                : "Connect Discogs to start syncing albums"}
            </div>
          </div>
          <div className="flex items-center gap-2">
            {me.isSuccess ? (
              <>
                <button
                  className="rounded-md border border-zinc-700 px-3 py-2 text-sm font-medium text-zinc-100 hover:bg-zinc-900"
                  onClick={() => syncAlbums.mutate()}
                  disabled={syncAlbums.isPending}
                >
                  {syncAlbums.isPending ? "Syncing…" : "Sync albums"}
                </button>
                <button
                  className="rounded-md bg-zinc-100 px-3 py-2 text-sm font-medium text-zinc-900 hover:bg-white"
                  onClick={() => logout.mutate()}
                  disabled={logout.isPending}
                >
                  Logout
                </button>
              </>
            ) : (
              <a
                className="rounded-md bg-zinc-100 px-3 py-2 text-sm font-medium text-zinc-900 hover:bg-white"
                href={`${apiUrl}/auth/discogs/start`}
              >
                Connect Discogs
              </a>
            )}
          </div>
        </div>
      </header>

      <main className="mx-auto max-w-4xl px-4 py-6">
        <div className="rounded-lg border border-zinc-800 bg-zinc-900/30 p-4">
          <div className="text-sm text-zinc-300">
            API health:{" "}
            {health.isLoading
              ? "checking…"
              : health.isError
                ? "error"
                : health.data}
          </div>
          {health.isError ? (
            <div className="mt-2 text-sm text-red-300">
              Make sure the Go API is running on {apiUrl}. (Try `api` in a flox
              shell.)
            </div>
          ) : null}
        </div>

        {me.isSuccess ? (
          <div className="mt-6 grid gap-4 md:grid-cols-2">
            <div className="rounded-lg border border-zinc-800 p-4">
              <div className="flex items-center justify-between">
                <div className="font-medium">Albums</div>
                <div className="text-xs text-zinc-400">
                  {albums.isLoading ? "Loading…" : `${albums.data?.length ?? 0} albums`}
                </div>
              </div>
              <div className="mt-3 grid gap-2">
                <input
                  className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm"
                  placeholder="Search by album or artist…"
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                />

                <div className="grid gap-2 md:grid-cols-2">
                  <select
                    className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm"
                    value={artistFilter}
                    onChange={(e) => setArtistFilter(e.target.value)}
                  >
                    <option value="">All artists</option>
                    {artistOptions.map((a) => (
                      <option key={a} value={a}>
                        {a}
                      </option>
                    ))}
                  </select>

                  <select
                    className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm"
                    value={`${sort}:${order}`}
                    onChange={(e) => {
                      const [s, o] = e.target.value.split(":") as [
                        "artist" | "title" | "spin_count" | "last_spun_at",
                        "asc" | "desc",
                      ];
                      setSort(s);
                      setOrder(o);
                    }}
                  >
                    <option value="artist:asc">Sort: Artist (A→Z)</option>
                    <option value="artist:desc">Sort: Artist (Z→A)</option>
                    <option value="title:asc">Sort: Title (A→Z)</option>
                    <option value="title:desc">Sort: Title (Z→A)</option>
                    <option value="spin_count:desc">Sort: Spins (high→low)</option>
                    <option value="spin_count:asc">Sort: Spins (low→high)</option>
                    <option value="last_spun_at:desc">Sort: Last spun (new→old)</option>
                    <option value="last_spun_at:asc">Sort: Last spun (old→new)</option>
                  </select>
                </div>

                <div className="flex flex-wrap items-center gap-2">
                  <div className="text-xs text-zinc-400">Filter tags:</div>
                  {tagOptions.map((t) => {
                    const active = tagFilterIDs.includes(t.id);
                    return (
                      <button
                        key={t.id}
                        className={`rounded-full border px-2 py-1 text-xs ${
                          active
                            ? "border-zinc-300 bg-zinc-100 text-zinc-900"
                            : "border-zinc-700 text-zinc-200 hover:bg-zinc-900"
                        }`}
                        onClick={() =>
                          setTagFilterIDs((prev) =>
                            prev.includes(t.id) ? prev.filter((x) => x !== t.id) : [...prev, t.id],
                          )
                        }
                        type="button"
                      >
                        {t.name}
                      </button>
                    );
                  })}
                </div>
              </div>

              {albums.isError ? (
                <div className="mt-2 text-sm text-red-300">{String(albums.error)}</div>
              ) : null}
              <div className="mt-3 max-h-[520px] overflow-auto">
                <ul className="space-y-2">
                  {(albums.data ?? []).map((a) => (
                    <li key={a.id} className="rounded-md border border-zinc-800 p-2">
                      <div className="flex items-start gap-3">
                      <div className="h-10 w-10 shrink-0 overflow-hidden rounded bg-zinc-800">
                        {a.thumb_url ? <img src={a.thumb_url} alt="" className="h-full w-full object-cover" /> : null}
                      </div>
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-sm font-medium">{a.artist}</div>
                        <div className="truncate text-sm text-zinc-300">{a.title}</div>
                        {a.record_label ? (
                          <div className="mt-1 text-xs text-zinc-500">Record label: {a.record_label}</div>
                        ) : null}
                        <div className="mt-1 text-xs text-zinc-500">
                          Spins: {a.spin_count}
                          {a.last_spun_at ? ` • Last: ${new Date(a.last_spun_at).toLocaleString()}` : ""}
                        </div>
                        <div className="mt-2 flex flex-wrap gap-1">
                          {(a.tags ?? []).map((t) => (
                            <button
                              key={t.id}
                              className="rounded-full border border-zinc-700 px-2 py-0.5 text-xs text-zinc-200 hover:bg-zinc-900"
                              title="Remove tag"
                              onClick={() => removeAlbumTag.mutate({ albumID: a.id, tagID: t.id })}
                              type="button"
                            >
                              {t.name}
                            </button>
                          ))}
                        </div>
                      </div>

                      <div className="flex flex-col items-end gap-2">
                        <button
                          className="rounded-md bg-zinc-100 px-2.5 py-1.5 text-xs font-medium text-zinc-900 hover:bg-white disabled:opacity-50"
                          onClick={() => {
                            setSelectedAlbumID(a.id);
                            createSpin.mutate({ album_id: a.id });
                          }}
                          disabled={createSpin.isPending}
                          type="button"
                        >
                          Add spin
                        </button>
                        {a.resource_url ? (
                          <a
                            className="text-xs text-zinc-300 underline decoration-zinc-600 underline-offset-2 hover:text-white"
                            href={a.resource_url}
                            target="_blank"
                            rel="noreferrer"
                          >
                            Release Info
                          </a>
                        ) : null}
                      </div>
                      </div>

                      <div className="mt-2 flex items-center gap-2">
                        <select
                          className="flex-1 rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1 text-xs"
                          defaultValue=""
                          onChange={(e) => {
                            const id = e.target.value;
                            if (!id) return;
                            addAlbumTag.mutate({ albumID: a.id, tag_id: id });
                            e.currentTarget.value = "";
                          }}
                        >
                          <option value="">Add existing tag…</option>
                          {tagOptions.map((t) => (
                            <option key={t.id} value={t.id}>
                              {t.name}
                            </option>
                          ))}
                        </select>
                        <button
                          className="rounded-md border border-zinc-700 px-2 py-1 text-xs text-zinc-200 hover:bg-zinc-900"
                          onClick={() => {
                            const name = window.prompt("New tag name?");
                            if (!name) return;
                            addAlbumTag.mutate({ albumID: a.id, name });
                          }}
                          type="button"
                        >
                          New…
                        </button>
                      </div>
                    </li>
                  ))}
                </ul>
              </div>
            </div>

            <div className="rounded-lg border border-zinc-800 p-4">
              <div className="font-medium">Spins</div>

              <div className="mt-3 rounded-md border border-zinc-800 p-3">
                <div className="text-sm font-medium">Create tag</div>
                <form
                  className="mt-2 flex gap-2"
                  onSubmit={(e) => {
                    e.preventDefault();
                    const name = newTagName.trim();
                    if (!name) return;
                    createTag.mutate({ name });
                    setNewTagName("");
                  }}
                >
                  <input
                    className="flex-1 rounded-md border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm"
                    placeholder="e.g. Jazz, Christmas…"
                    value={newTagName}
                    onChange={(e) => setNewTagName(e.target.value)}
                  />
                  <button
                    className="rounded-md bg-zinc-100 px-3 py-2 text-sm font-medium text-zinc-900 hover:bg-white disabled:opacity-50"
                    disabled={!newTagName.trim() || createTag.isPending}
                    type="submit"
                  >
                    Add
                  </button>
                </form>
                {createTag.isError ? (
                  <div className="mt-2 text-sm text-red-300">{String(createTag.error)}</div>
                ) : null}
              </div>

              <form
                className="mt-3 space-y-2"
                onSubmit={(e) => {
                  e.preventDefault();
                  if (!selectedAlbumID) return;
                  const spunAt = spunAtLocal ? new Date(spunAtLocal).toISOString() : undefined;
                  createSpin.mutate({
                    album_id: selectedAlbumID,
                    spun_at: spunAt,
                    note: note.trim() ? note.trim() : undefined,
                  });
                  setNote("");
                  setSpunAtLocal("");
                }}
              >
                <select
                  className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm"
                  value={selectedAlbumID}
                  onChange={(e) => setSelectedAlbumID(e.target.value)}
                >
                  <option value="">Select an album…</option>
                  {albumOptions.map((o) => (
                    <option key={o.id} value={o.id}>
                      {o.label}
                    </option>
                  ))}
                </select>
                <input
                  className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm"
                  type="datetime-local"
                  value={spunAtLocal}
                  onChange={(e) => setSpunAtLocal(e.target.value)}
                />
                <input
                  className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm"
                  placeholder="Note (optional)"
                  value={note}
                  onChange={(e) => setNote(e.target.value)}
                />
                <button
                  className="w-full rounded-md bg-zinc-100 px-3 py-2 text-sm font-medium text-zinc-900 hover:bg-white disabled:opacity-50"
                  disabled={!selectedAlbumID || createSpin.isPending}
                  type="submit"
                >
                  {createSpin.isPending ? "Saving…" : "Add spin"}
                </button>
                {createSpin.isError ? (
                  <div className="text-sm text-red-300">{String(createSpin.error)}</div>
                ) : null}
              </form>

              <div className="mt-4">
                {spins.isError ? (
                  <div className="text-sm text-red-300">{String(spins.error)}</div>
                ) : null}
                <div className="max-h-[360px] overflow-auto">
                  <ul className="space-y-2">
                    {(spins.data ?? []).map((s) => (
                      <li key={s.id} className="rounded-md border border-zinc-800 p-2">
                        <div className="flex items-start justify-between gap-2">
                          <div className="min-w-0">
                            <div className="truncate text-sm font-medium">{s.album_artist}</div>
                            <div className="truncate text-sm text-zinc-300">{s.album_title}</div>
                            <div className="mt-1 text-xs text-zinc-500">
                              {new Date(s.spun_at).toLocaleString()}
                              {s.note ? ` • ${s.note}` : ""}
                            </div>
                          </div>
                          <button
                            className="text-xs text-zinc-300 underline decoration-zinc-600 underline-offset-2 hover:text-white disabled:opacity-50"
                            onClick={() => deleteSpin.mutate(s.id)}
                            disabled={deleteSpin.isPending}
                          >
                            Delete
                          </button>
                        </div>
                      </li>
                    ))}
                  </ul>
                </div>
              </div>
            </div>
          </div>
        ) : me.isError ? (
          <div className="mt-6 rounded-lg border border-zinc-800 p-4 text-sm text-zinc-300">
            Not connected. Click <span className="font-medium">Connect Discogs</span> above to authenticate.
          </div>
        ) : (
          <div className="mt-6 rounded-lg border border-zinc-800 p-4 text-sm text-zinc-300">Loading session…</div>
        )}
      </main>
    </div>
  );
}

