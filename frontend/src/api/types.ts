// Raw Jellyfin wire-format types (PascalCase, matches the Go backend's
// internal/controllers/items_controller.go jellyfinItem exactly). Kept
// separate from the friendly `Movie` type below so the rest of the app
// never has to think about Jellyfin's field casing.

export interface JellyfinImageTags {
  Primary?: string;
}

export interface JellyfinPerson {
  Id: string;
  Name: string;
  Type: string;
  Role?: string;
}

export interface JellyfinMediaStream {
  Type: string;
  Codec: string;
}

export type MovieStatus =
  | "pending"
  | "probing"
  | "needs_transcode"
  | "transcoding"
  | "ready"
  | "error";

export interface JellyfinItem {
  Id: string;
  Name: string;
  Type: string;
  Overview?: string;
  ProductionYear?: number;
  RunTimeTicks?: number;
  DateCreated?: string;
  Genres?: string[];
  ImageTags?: JellyfinImageTags;
  BackdropImageTags?: string[];
  People?: JellyfinPerson[];
  ProviderIds?: { Tmdb?: string };
  MediaStreams?: JellyfinMediaStream[];

  // Music fields (real Jellyfin field names)
  AlbumArtist?: string;
  Album?: string;
  IndexNumber?: number;
  ParentIndexNumber?: number;

  MagicBoxieStatus: MovieStatus;
  MagicBoxieProgressPercent?: number | null;
  MagicBoxieOriginalFilename: string;
  MagicBoxieNeedsReview: boolean;
  MagicBoxiePosterIsGenerated: boolean;
  MagicBoxieSyncEnabled: boolean;
}

export interface ItemsResponse {
  Items: JellyfinItem[];
  TotalRecordCount: number;
  StartIndex: number;
}

// ---- Friendly internal shape used by the rest of the app ----

export interface Movie {
  id: number;
  title: string;
  year: number;
  tmdbId: number | null;
  overview: string;
  genres: string[];
  cast: { name: string; role?: string }[];
  runtimeSeconds: number;
  hasPoster: boolean;
  hasBackdrop: boolean;
  posterTag: string;
  backdropTag: string;
  status: MovieStatus;
  needsReview: boolean;
  posterIsGenerated: boolean;
  originalFilename: string;
  addedAt: string;
  syncEnabled: boolean;
}

const TICKS_PER_SECOND = 10_000_000;

// Item ids are "<kind>-<n>" (e.g. "movie-5", "track-12") since movies,
// artists, albums, and tracks each have their own auto-incrementing primary
// key and would otherwise collide in Jellyfin's flat item-id space. Movies
// keep a plain numeric `id` internally (this app only dealt with movies
// until the music library shipped); music entities keep the full string id.
export function parseNumericId(rawId: string): number {
  const [, numStr] = rawId.split("-");
  return Number(numStr ?? rawId);
}

export function mapItemToMovie(item: JellyfinItem): Movie {
  return {
    id: parseNumericId(item.Id),
    title: item.Name,
    year: item.ProductionYear ?? 0,
    tmdbId: item.ProviderIds?.Tmdb ? Number(item.ProviderIds.Tmdb) : null,
    overview: item.Overview ?? "",
    genres: item.Genres ?? [],
    cast: (item.People ?? []).map((p) => ({ name: p.Name, role: p.Role })),
    runtimeSeconds: (item.RunTimeTicks ?? 0) / TICKS_PER_SECOND,
    hasPoster: Boolean(item.ImageTags?.Primary),
    hasBackdrop: Boolean(item.BackdropImageTags && item.BackdropImageTags.length > 0),
    posterTag: item.ImageTags?.Primary ?? "",
    backdropTag: item.BackdropImageTags?.[0] ?? "",
    status: item.MagicBoxieStatus || "ready",
    needsReview: Boolean(item.MagicBoxieNeedsReview),
    posterIsGenerated: Boolean(item.MagicBoxiePosterIsGenerated),
    originalFilename: item.MagicBoxieOriginalFilename ?? "",
    addedAt: item.DateCreated ?? new Date(0).toISOString(),
    syncEnabled: Boolean(item.MagicBoxieSyncEnabled),
  };
}

// A magicboxie-device Pi that has checked in via POST /devices/register -
// see GET /api/devices and models.Device's own doc comment (purely for
// visibility, not access control - there's no pairing/approval step).
export interface Device {
  id: number;
  device_id: string;
  last_seen_at: string;
}

export interface Artist {
  id: number;
  name: string;
  hasImage: boolean;
}

export interface Album {
  id: number;
  title: string;
  year: number;
  artistName: string;
  hasCover: boolean;
}

export interface Track {
  id: number;
  title: string;
  albumTitle: string;
  artistName: string;
  trackNumber: number;
  discNumber: number;
  durationSeconds: number;
}

export function mapItemToArtist(item: JellyfinItem): Artist {
  return {
    id: parseNumericId(item.Id),
    name: item.Name,
    hasImage: Boolean(item.ImageTags?.Primary),
  };
}

export function mapItemToAlbum(item: JellyfinItem): Album {
  return {
    id: parseNumericId(item.Id),
    title: item.Name,
    year: item.ProductionYear ?? 0,
    artistName: item.AlbumArtist ?? "",
    hasCover: Boolean(item.ImageTags?.Primary),
  };
}

export function mapItemToTrack(item: JellyfinItem): Track {
  return {
    id: parseNumericId(item.Id),
    title: item.Name,
    albumTitle: item.Album ?? "",
    artistName: item.AlbumArtist ?? "",
    trackNumber: item.IndexNumber ?? 0,
    discNumber: item.ParentIndexNumber ?? 0,
    durationSeconds: (item.RunTimeTicks ?? 0) / TICKS_PER_SECOND,
  };
}

// ---- Manual TMDB re-match (MagicBoxie-specific, not part of Jellyfin) ----

export interface TMDBSearchResult {
  tmdb_id: number;
  title: string;
  year?: number;
  overview?: string;
  poster_url?: string;
}

export interface BackgroundJob {
  id: number;
  movie_id: number;
  movie_title: string;
  type: string;
  status: "queued" | "running" | "completed" | "failed";
  progress_percent: number;
  log_tail?: string;
  started_at?: string;
  finished_at?: string;
  created_at: string;
}

export interface ThumbnailCandidate {
  index: number;
  url: string;
  selected: boolean;
}

// ---- SSE job-progress events (MagicBoxie-specific, not part of Jellyfin) ----

export interface JobProgressEvent {
  type: "job_progress";
  data: { movie_id: number; job_id: number; progress_percent: number };
}
export interface JobCompletedEvent {
  type: "job_completed";
  data: { movie_id: number; job_id: number; status: MovieStatus };
}
export interface JobFailedEvent {
  type: "job_failed";
  data: { movie_id: number; job_id: number; error: string };
}
export type ServerEvent = JobProgressEvent | JobCompletedEvent | JobFailedEvent;

// GET /api/info -- the media server Pi's own health for the admin Info page.
// Readings the Pi can't provide (e.g. in the Docker dev container) are null.
export interface ServerInfo {
  system: {
    hostname: string;
    mdns_name: string;
    model: string | null;
    os: string | null;
    kernel: string | null;
    arch: string;
    cpu_count: number;
    addresses: { interface: string; address: string }[];
    uptime_seconds: number | null;
    server_uptime_seconds: number;
    load_average: number[] | null;
    cpu_temperature_celsius: number | null;
    under_voltage: boolean | null;
    throttled: boolean | null;
    memory_mb: { total: number; available: number } | null;
    swap_mb: { total: number; available: number } | null;
    disks: { label: string; path: string; total: number; free: number }[];
    software: { commit?: string; date?: string; modified?: boolean; go_version: string };
  };
  library: { movies: number; tracks: number; devices: number; jobs_active: number };
}
