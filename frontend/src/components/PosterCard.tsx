import { Link } from "react-router-dom";
import type { Movie } from "../api/types";
import { movieImageUrl } from "../api/client";
import { useTranscodeProgress } from "../hooks/useTranscodeProgress";

function StatusBadge({ movie }: { movie: Movie }) {
  const progress = useTranscodeProgress(movie.id);
  const playerProgress = useTranscodeProgress(movie.id, "playerProgress");

  if (movie.status === "pending" || movie.status === "probing") {
    return (
      <div className="absolute inset-x-0 bottom-0 bg-black/80 px-2 py-1 text-xs text-neutral-200">
        Matching metadata…
      </div>
    );
  }
  if (movie.status === "needs_transcode") {
    return (
      <div className="absolute inset-x-0 bottom-0 bg-black/80 px-2 py-1 text-xs text-neutral-200">
        Queued for transcode
      </div>
    );
  }
  if (movie.status === "transcoding") {
    const pct = progress ?? 0;
    return (
      <div className="absolute inset-x-0 bottom-0 bg-black/80 px-2 py-1">
        <div className="mb-1 text-xs text-neutral-200">Transcoding {pct.toFixed(0)}%</div>
        <div className="h-1 w-full overflow-hidden rounded-full bg-neutral-700">
          <div
            className="h-full bg-red-600 transition-all duration-500"
            style={{ width: `${Math.min(100, Math.max(0, pct))}%` }}
          />
        </div>
      </div>
    );
  }
  if (movie.status === "ready" && movie.playerStatus === "pending") {
    const pct = playerProgress;
    return (
      <div className="absolute inset-x-0 bottom-0 bg-black/80 px-2 py-1">
        <div className="mb-1 text-xs text-neutral-200">
          {pct === undefined ? "Queued for 480p" : `Making 480p ${pct.toFixed(0)}%`}
        </div>
        <div className="h-1 w-full overflow-hidden rounded-full bg-neutral-700">
          <div
            className="h-full bg-red-600 transition-all duration-500"
            style={{ width: `${Math.min(100, Math.max(0, pct ?? 0))}%` }}
          />
        </div>
      </div>
    );
  }
  if (movie.status === "error") {
    return (
      <div className="absolute inset-x-0 bottom-0 bg-red-900/90 px-2 py-1 text-xs text-red-100">
        Failed
      </div>
    );
  }
  return null;
}

export function PosterCard({ movie, onSelect }: { movie: Movie; onSelect?: (movie: Movie) => void }) {
  return (
    <Link
      to={`/movies/${movie.id}`}
      onMouseEnter={() => onSelect?.(movie)}
      onFocus={() => onSelect?.(movie)}
      className="group relative block w-full overflow-hidden rounded bg-neutral-900 shadow-lg ring-1 ring-white/5 transition duration-300 ease-out hover:z-10 hover:scale-110 hover:shadow-[0_18px_50px_rgba(0,0,0,.75)] hover:ring-white/20 focus:z-10 focus:scale-105 focus:outline-none focus:ring-2 focus:ring-white"
    >
      <div className="aspect-[2/3] w-full bg-neutral-800">
        {movie.hasPoster ? (
          <img
            src={movieImageUrl(movie.id, "primary", movie.posterTag)}
            alt={movie.title}
            loading="lazy"
            className="h-full w-full object-cover"
          />
        ) : (
          <div className="flex h-full w-full items-center justify-center px-2 text-center text-sm text-neutral-500">
            {movie.title}
          </div>
        )}
      </div>
      <div className="pointer-events-none absolute inset-x-0 bottom-0 translate-y-3 bg-gradient-to-t from-black via-black/90 to-transparent px-3 pb-3 pt-12 text-xs text-white opacity-0 transition duration-300 group-hover:translate-y-0 group-hover:opacity-100">
        <p className="truncate font-bold">{movie.title}</p>
        <p className="mt-1 flex gap-2 text-[11px] text-neutral-300">
          {movie.year > 0 && <span>{movie.year}</span>}
          {movie.genres[0] && <span>{movie.genres[0]}</span>}
        </p>
      </div>
      <StatusBadge movie={movie} />
    </Link>
  );
}
