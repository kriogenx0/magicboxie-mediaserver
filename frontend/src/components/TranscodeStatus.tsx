import { Link } from "react-router-dom";
import { useBackgroundJobs } from "../hooks/useMovies";

/** Header indicator for the job being transcoded right now (a full transcode
 * or a 480p player copy), with how many more are waiting. Hidden when idle. */
export function TranscodeStatus() {
  const { data: jobs = [] } = useBackgroundJobs();
  const running = jobs.find((j) => j.status === "running");
  const queued = jobs.filter((j) => j.status === "queued").length;
  if (!running && queued === 0) return null;

  const percent = running ? Math.max(0, Math.min(100, running.progress_percent)) : 0;
  const kind = running?.type === "player_transcode" ? "480p copy" : "Transcoding";
  // The jobs list is capped server-side, so a full list means "at least".
  const more = queued > 0 ? ` · ${queued}${jobs.length >= 50 ? "+" : ""} queued` : "";
  const title = running ? `${kind}: ${running.movie_title} ${percent.toFixed(0)}%${more}` : `${queued} queued`;

  return (
    <Link
      to="/admin/activity"
      title={title}
      className="flex min-w-0 flex-col gap-1 rounded-sm px-1 text-xs text-neutral-300 transition hover:text-white sm:w-52"
    >
      <span className="flex items-center justify-between gap-2 whitespace-nowrap">
        <span className="hidden min-w-0 truncate sm:inline">{running ? `${kind} · ${running.movie_title}` : "Queued"}</span>
        <span className="tabular-nums">{running ? `${percent.toFixed(0)}%` : queued}</span>
      </span>
      <span className="h-1 w-12 overflow-hidden rounded-full bg-neutral-700 sm:w-full">
        <span className="block h-full bg-[#e50914] transition-all duration-500" style={{ width: `${percent}%` }} />
      </span>
      {queued > 0 && running && <span className="hidden text-[10px] text-neutral-500 sm:block">{queued}{jobs.length >= 50 ? "+" : ""} more queued</span>}
    </Link>
  );
}
