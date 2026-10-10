import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { getToken } from "../api/client";
import type { ServerEvent } from "../api/types";

/**
 * Subscribes to the backend's SSE job-progress stream and invalidates the
 * relevant react-query cache entries on each event, so movie cards/detail
 * pages pick up live "Transcoding NN%..." -> "ready" transitions without
 * polling. A dropped connection is self-healing: EventSource auto-reconnects,
 * and the next invalidated fetch always reflects true server state.
 */
export function useLiveEvents() {
  const queryClient = useQueryClient();

  useEffect(() => {
    const token = getToken();
    if (!token) return;

    const source = new EventSource(`/api/events?api_key=${encodeURIComponent(token)}`);

    source.onmessage = (e) => {
      let evt: ServerEvent;
      try {
        evt = JSON.parse(e.data);
      } catch {
        return;
      }

      if (evt.type === "job_progress") {
        // Ephemeral, SSE-only state: not worth a REST round trip per
        // update, so it's pushed straight into the query cache as a
        // synthetic entry that PosterCard/MovieDetailPage subscribe to.
        const key = evt.data.job_type === "player_transcode" ? "playerProgress" : "transcodeProgress";
        queryClient.setQueryData([key, evt.data.movie_id], evt.data.progress_percent);
        queryClient.invalidateQueries({ queryKey: ["backgroundJobs"] });
        return;
      }

      queryClient.invalidateQueries({ queryKey: ["backgroundJobs"] });
      queryClient.invalidateQueries({ queryKey: ["movies", evt.data.movie_id] });
      queryClient.invalidateQueries({ queryKey: ["movies"] });
    };

    return () => source.close();
  }, [queryClient]);
}
