import { useEffect, useRef, useState } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { getThumbnailCandidates, useMovie, useRenameMovie, useSelectThumbnail, useSetDeviceSync } from "../hooks/useMovies";
import { movieImageUrl, movieStreamUrl, movieDownloadUrl } from "../api/client";
import type { ThumbnailCandidate } from "../api/types";
import { useTranscodeProgress } from "../hooks/useTranscodeProgress";
import { MatchPicker } from "../components/MatchPicker";
import { PageLoader } from "../components/PageLoader";

function formatRuntime(seconds: number): string {
  const mins = Math.round(seconds / 60);
  const h = Math.floor(mins / 60);
  const m = mins % 60;
  return h === 0 ? `${m}m` : `${h}h ${m}m`;
}

export function MovieDetailPage() {
  const { id } = useParams<{ id: string }>();
  const movieId = Number(id);
  const { data: movie, isLoading, error } = useMovie(movieId);
  const [showPlayer, setShowPlayer] = useState(false);
  const [showMatchPicker, setShowMatchPicker] = useState(false);
  const [posterVersion, setPosterVersion] = useState(0);
  const [generatedBackdropUrl, setGeneratedBackdropUrl] = useState<string | null>(null);
  const [thumbnailCandidates, setThumbnailCandidates] = useState<ThumbnailCandidate[]>([]);
  const [thumbnailError, setThumbnailError] = useState<string | null>(null);
  const [isRenaming, setIsRenaming] = useState(false);
  const [titleDraft, setTitleDraft] = useState("");
  const videoRef = useRef<HTMLVideoElement>(null);
  const navigate = useNavigate();
  const progress = useTranscodeProgress(movieId);
  const playerProgress = useTranscodeProgress(movieId, "playerProgress");
  const selectThumbnail = useSelectThumbnail(movieId);
  const setDeviceSync = useSetDeviceSync(movieId);
  const renameMovie = useRenameMovie(movieId);

  useEffect(() => {
    if (movie && !isRenaming) setTitleDraft(movie.title);
  }, [movie?.title, isRenaming]);

  useEffect(() => {
    if (!showPlayer) return;
    const previous = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = previous;
    };
  }, [showPlayer]);

  useEffect(() => {
    let cancelled = false;
    if (!movie?.posterIsGenerated) {
      setGeneratedBackdropUrl(null);
      setThumbnailCandidates([]);
      return;
    }

    getThumbnailCandidates(movie.id)
      .then((candidates) => {
        if (cancelled) return;
        setThumbnailCandidates(candidates);
        setThumbnailError(null);
        // Prefer a later frame for the wide hero, but never reuse the frame
        // currently selected as the poster.
        const backdrop = [...candidates].reverse().find((candidate) => !candidate.selected);
        setGeneratedBackdropUrl(backdrop?.url ?? null);
      })
      .catch(() => {
        if (!cancelled) {
          setGeneratedBackdropUrl(null);
          setThumbnailError("Could not load thumbnail choices.");
        }
      });
    return () => {
      cancelled = true;
    };
  }, [movie?.id, movie?.posterIsGenerated, posterVersion]);

  if (isLoading) {
    return <PageLoader label="Loading movie" />;
  }
  if (error || !movie) {
    return <div className="p-8 text-red-500">Movie not found.</div>;
  }

  const ready = movie.status === "ready";

  const enterFullscreen = async () => {
    const video = videoRef.current;
    if (!video) return;
    await video.requestFullscreen?.().catch(() => {
      // Fullscreen can be unavailable in an embedded browser; native video
      // controls still expose the platform's fullscreen option where present.
    });
  };

  const saveTitle = async () => {
    const title = titleDraft.trim();
    if (!title || title === movie.title) {
      setTitleDraft(movie.title);
      setIsRenaming(false);
      return;
    }
    await renameMovie.mutateAsync(title);
    setIsRenaming(false);
  };

  return (
    <div>
      <div className="relative h-[55vh] min-h-[350px] w-full overflow-hidden sm:h-[65vh]">
        {movie.hasBackdrop ? (
          <img
            src={movieImageUrl(movie.id, "backdrop", movie.backdropTag)}
            alt=""
            className="absolute inset-0 h-full w-full object-cover"
          />
        ) : generatedBackdropUrl ? (
          <img
            src={`${generatedBackdropUrl}?v=${posterVersion}`}
            alt=""
            className="absolute inset-0 h-full w-full object-cover"
          />
        ) : (
          <div className="absolute inset-0 bg-gradient-to-br from-neutral-800 to-black" />
        )}
        <div className="absolute inset-0 bg-gradient-to-t from-[#141414] via-black/45 to-transparent" />
        <div className="absolute inset-0 bg-gradient-to-r from-black/55 to-transparent" />
        <button
          onClick={() => navigate(-1)}
          className="absolute left-4 top-20 rounded-sm bg-black/50 px-3 py-1.5 text-sm text-white transition hover:bg-black/80 sm:left-10 sm:top-24"
        >
          ← Back
        </button>
      </div>

      <div className="relative z-10 mx-auto -mt-44 max-w-6xl px-4 pb-16 sm:-mt-60 sm:px-10">
        <div className="flex flex-col gap-6 sm:flex-row">
          <div className="hidden w-40 shrink-0 overflow-hidden rounded-sm bg-neutral-800 shadow-2xl sm:block sm:w-56">
            <div className="aspect-[2/3] w-full">
              {movie.hasPoster ? (
                <img
                  src={`${movieImageUrl(movie.id, "primary", movie.posterTag)}${movie.posterTag ? "&" : "?"}v=${posterVersion}`}
                  alt={movie.title}
                  className="h-full w-full object-cover"
                />
              ) : (
                <div className="flex h-full w-full items-center justify-center px-2 text-center text-sm text-neutral-500">
                  {movie.title}
                </div>
              )}
            </div>
          </div>

          <div className="flex-1">
            <div className="group/title flex max-w-3xl items-center gap-3">
              {isRenaming ? (
                <form
                  className="flex w-full items-center gap-2"
                  onSubmit={(event) => {
                    event.preventDefault();
                    void saveTitle();
                  }}
                >
                  <input
                    autoFocus
                    value={titleDraft}
                    maxLength={200}
                    onChange={(event) => setTitleDraft(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Escape") {
                        setTitleDraft(movie.title);
                        setIsRenaming(false);
                      }
                    }}
                    className="min-w-0 flex-1 border-b-2 border-[#e50914] bg-black/55 px-2 py-1 text-3xl font-extrabold tracking-tight text-white outline-none backdrop-blur sm:text-5xl"
                    aria-label="Movie title"
                  />
                  <button disabled={renameMovie.isPending} className="rounded bg-white px-4 py-2 text-sm font-bold text-black disabled:opacity-50">
                    Save
                  </button>
                  <button type="button" onClick={() => setIsRenaming(false)} className="rounded bg-white/15 px-4 py-2 text-sm font-bold text-white">
                    Cancel
                  </button>
                </form>
              ) : (
                <>
                  <h1 className="text-4xl font-extrabold tracking-tight sm:text-5xl">{movie.title}</h1>
                  <button
                    onClick={() => setIsRenaming(true)}
                    className="rounded-full bg-black/40 p-2 text-neutral-300 opacity-100 backdrop-blur transition hover:bg-white hover:text-black sm:opacity-0 sm:group-hover/title:opacity-100"
                    aria-label="Rename movie"
                    title="Rename movie"
                  >
                    ✎
                  </button>
                </>
              )}
            </div>
            {renameMovie.isError && (
              <p className="mt-2 text-sm text-red-400">
                {renameMovie.error instanceof Error ? renameMovie.error.message : "Could not rename movie."}
              </p>
            )}
            <div className="mt-3 flex flex-wrap items-center gap-3 text-sm font-medium text-neutral-300">
              {movie.year > 0 && <span>{movie.year}</span>}
              {movie.runtimeSeconds > 0 && <span>{formatRuntime(movie.runtimeSeconds)}</span>}
              {movie.genres.length > 0 && <span>{movie.genres.join(", ")}</span>}
            </div>

            {!ready && (
              <div className="mt-5 rounded-sm bg-neutral-900/90 px-4 py-3 text-sm text-neutral-300">
                {movie.status === "transcoding"
                  ? `Transcoding… ${(progress ?? 0).toFixed(0)}%`
                  : movie.status === "needs_transcode"
                    ? "Queued for transcode"
                    : movie.status === "error"
                      ? "This file failed to process."
                      : "Matching metadata…"}
              </div>
            )}

            {ready && movie.playerStatus === "pending" && (
              <div className="mt-5 rounded-sm bg-neutral-900/90 px-4 py-3 text-sm text-neutral-300">
                {playerProgress === undefined
                  ? "Queued for the 480p player copy"
                  : `Making the 480p player copy… ${playerProgress.toFixed(0)}%`}
              </div>
            )}

            {(!movie.tmdbId || movie.needsReview) && (
              <div className="mt-5 flex items-center justify-between gap-4 rounded-sm bg-neutral-900/90 px-4 py-3 text-sm text-neutral-300">
                <span>
                  {movie.tmdbId
                    ? "This match might not be right."
                    : "No metadata match was found for this file."}
                </span>
                <button
                  onClick={() => setShowMatchPicker(true)}
                  className="shrink-0 rounded-sm border border-neutral-500 px-3 py-1.5 text-neutral-200 transition hover:border-white hover:text-white"
                >
                  Search TMDB
                </button>
              </div>
            )}

            {movie.overview && <p className="mt-5 max-w-2xl leading-relaxed text-neutral-200">{movie.overview}</p>}

            {movie.posterIsGenerated && (
              <section className="mt-6">
                <h2 className="text-sm font-semibold uppercase tracking-wide text-neutral-300">
                  Choose thumbnail
                </h2>
                <p className="mt-1 text-sm text-neutral-400">
                  Select a frame to use as the movie poster.
                </p>
                {thumbnailError && <p className="mt-3 text-sm text-red-400">{thumbnailError}</p>}
                {selectThumbnail.isError && (
                  <p className="mt-3 text-sm text-red-400">
                    {selectThumbnail.error instanceof Error
                      ? selectThumbnail.error.message
                      : "Could not select thumbnail."}
                  </p>
                )}
                <div className="mt-3 grid max-w-3xl grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
                  {thumbnailCandidates.map((candidate) => (
                    <button
                      key={candidate.index}
                      onClick={() =>
                        selectThumbnail.mutate(candidate.index, {
                          onSuccess: () => setPosterVersion((version) => version + 1),
                        })
                      }
                      disabled={selectThumbnail.isPending}
                      className={`group overflow-hidden rounded-sm border-2 bg-neutral-800 text-left transition hover:border-white disabled:opacity-50 ${
                        candidate.selected ? "border-[#e50914]" : "border-transparent"
                      }`}
                    >
                      <img
                        src={candidate.url}
                        alt={`Thumbnail option ${candidate.index + 1}`}
                        className="aspect-video w-full object-cover"
                      />
                      <span className="block px-2 py-1.5 text-center text-xs font-medium text-neutral-300 group-hover:text-white">
                        {candidate.selected ? "Current" : "Select"}
                      </span>
                    </button>
                  ))}
                </div>
              </section>
            )}

            {ready && (
              <div className="mt-7 flex flex-wrap gap-3 whitespace-nowrap">
                <button
                  onClick={() => setShowPlayer(true)}
                  className="rounded-sm bg-white px-5 py-2.5 font-bold text-black transition hover:bg-white/80 sm:px-6"
                >
                  ▶ Play
                </button>
                <a
                  href={movieDownloadUrl(movie.id)}
                  className="rounded-sm bg-neutral-500/70 px-6 py-2.5 font-bold text-white transition hover:bg-neutral-500/50"
                >
                  Download
                </a>
                <button
                  onClick={() => setDeviceSync.mutate(!movie.syncEnabled)}
                  disabled={setDeviceSync.isPending}
                  aria-pressed={movie.syncEnabled}
                  className={`rounded-sm border px-6 py-2.5 font-bold transition disabled:opacity-50 ${
                    movie.syncEnabled
                      ? "border-transparent bg-[#e50914] text-white hover:bg-[#f6121d]"
                      : "border-neutral-500 text-neutral-100 hover:border-white"
                  }`}
                >
                  {movie.syncEnabled ? "✓ Synced to device" : "Sync to device"}
                </button>
              </div>
            )}

            {movie.cast.length > 0 && (
              <div className="mt-8">
                <h2 className="mb-2 text-sm font-semibold uppercase tracking-wide text-neutral-400">
                  Cast
                </h2>
                <p className="text-neutral-300">{movie.cast.slice(0, 8).map((c) => c.name).join(", ")}</p>
              </div>
            )}
          </div>
        </div>
      </div>

      {showPlayer && (
        <div className="fixed inset-0 z-50 flex h-[100dvh] w-screen items-center justify-center overflow-hidden bg-black">
          <div className="absolute inset-x-0 top-0 z-10 flex h-16 items-center justify-between border-b border-white/10 bg-black/85 px-4 shadow-xl backdrop-blur-md sm:h-[72px] sm:px-6">
            <p className="max-w-[55%] truncate text-sm font-semibold text-white sm:max-w-[70%] sm:text-base">{movie.title}</p>
            <div className="flex items-center gap-2">
              <button
                onClick={enterFullscreen}
                className="rounded-sm bg-white/10 px-3 py-1.5 text-sm text-white transition hover:bg-white/20"
              >
                ⛶ Full screen
              </button>
              <button
                onClick={() => setShowPlayer(false)}
                className="rounded bg-white px-4 py-2 text-sm font-bold text-black transition hover:bg-neutral-200 sm:px-5"
              >
                <span aria-hidden="true">✕</span> Close
              </button>
            </div>
          </div>
          {/* eslint-disable-next-line jsx-a11y/media-has-caption */}
          <video
            ref={videoRef}
            key={movie.id}
            src={movieStreamUrl(movie.id)}
            controls
            autoPlay
            className="h-[100dvh] w-screen object-contain"
          />
        </div>
      )}

      {showMatchPicker && (
        <MatchPicker
          movieId={movie.id}
          initialQuery={movie.title}
          initialYear={movie.year || undefined}
          onClose={() => setShowMatchPicker(false)}
        />
      )}

    </div>
  );
}
