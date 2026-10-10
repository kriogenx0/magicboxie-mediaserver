import { useQuery } from "@tanstack/react-query";

/** Reads the live transcode percentage pushed into the cache by
 * useLiveEvents (see hooks/useLiveEvents.ts). Never fetches over the
 * network -- it's a pure SSE-driven cache read. */
export function useTranscodeProgress(movieId: number, kind: "transcodeProgress" | "playerProgress" = "transcodeProgress"): number | undefined {
  const { data } = useQuery<number>({
    queryKey: [kind, movieId],
    queryFn: () => Promise.resolve(0),
    enabled: false,
    staleTime: Infinity,
  });
  return data;
}
