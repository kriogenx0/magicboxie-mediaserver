import { useMemo, useState } from "react";
import { useMovies } from "../hooks/useMovies";
import { Hero } from "../components/Hero";
import { PosterRow } from "../components/PosterRow";
import type { Movie } from "../api/types";
import { PageLoader } from "../components/PageLoader";

export function HomePage() {
  const { data: movies, isLoading, error } = useMovies();
  const [selectedMovie, setSelectedMovie] = useState<Movie | null>(null);

  const { heroMovies, recentlyAdded, inProgress, kids, byGenre } = useMemo(() => {
    const all = movies ?? [];
    const ready = all.filter((m) => m.status === "ready");
    const inProgress = all.filter((m) =>
      ["pending", "probing", "needs_transcode", "transcoding"].includes(m.status),
    );
    const recentlyAdded = [...all].sort(
      (a, b) => new Date(b.addedAt).getTime() - new Date(a.addedAt).getTime(),
    );

    // TMDB has no single "kids" genre, so Animation/Family (its closest
    // equivalents) are folded into one "Kids" row instead of two separate
    // genre rows, and excluded below from the generic per-genre grouping.
    const KIDS_GENRES = new Set(["Animation", "Family"]);
    const kids = ready.filter((m) => m.genres.some((g) => KIDS_GENRES.has(g)));

    const genreMap = new Map<string, Movie[]>();
    for (const m of ready) {
      for (const genre of m.genres.length > 0 ? m.genres : ["Unsorted"]) {
        if (KIDS_GENRES.has(genre)) continue;
        const bucket = genreMap.get(genre) ?? [];
        bucket.push(m);
        genreMap.set(genre, bucket);
      }
    }

    return {
      heroMovies: ready.slice(0, 5),
      recentlyAdded,
      inProgress,
      kids,
      byGenre: Array.from(genreMap.entries()),
    };
  }, [movies]);

  if (isLoading) {
    return <PageLoader label="Loading your library" />;
  }
  if (error) {
    return <div className="p-8 text-red-500">Failed to load movies.</div>;
  }
  if ((movies ?? []).length === 0) {
    return (
      <div className="p-8 text-center text-neutral-400">
        No movies yet. Use "Add Media" above to upload your first one.
      </div>
    );
  }

  return (
    <div>
      <Hero movies={heroMovies} selectedMovie={selectedMovie} />
      <div className="relative z-10 -mt-14 sm:-mt-20">
        <PosterRow title="Continue Processing" movies={inProgress} onSelect={setSelectedMovie} />
        <PosterRow title="Recently Added" movies={recentlyAdded} onSelect={setSelectedMovie} />
        <PosterRow title="Kids" movies={kids} onSelect={setSelectedMovie} />
        {byGenre.map(([label, list]) => (
          <PosterRow key={label} title={label} movies={list} onSelect={setSelectedMovie} />
        ))}
      </div>
    </div>
  );
}
