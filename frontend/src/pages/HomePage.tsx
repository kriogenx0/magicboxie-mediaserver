import { useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useMovies } from "../hooks/useMovies";
import { Hero } from "../components/Hero";
import { PosterCard } from "../components/PosterCard";
import { PosterRow } from "../components/PosterRow";
import type { Movie } from "../api/types";
import { PageLoader } from "../components/PageLoader";

export function HomePage() {
  const { data: movies, isLoading, error } = useMovies();
  const [selectedMovie, setSelectedMovie] = useState<Movie | null>(null);
  const [params, setParams] = useSearchParams();
  const view = params.get("view") === "all" ? "all" : "category";
  const setView = (next: "category" | "all") =>
    setParams(next === "all" ? { view: "all" } : {}, { replace: true });

  const { heroMovies, allTitles, recentlyAdded, inProgress, kids, byGenre } = useMemo(() => {
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
      allTitles: [...all].sort((a, b) => a.title.localeCompare(b.title, undefined, { sensitivity: "base" })),
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
      <div className="p-8 pt-32 text-center text-neutral-400">
        No movies yet. Use "Add Media" above to upload your first one.
      </div>
    );
  }

  return (
    <div>
      <Hero movies={heroMovies} selectedMovie={selectedMovie} />
      <div className="relative z-10 -mt-6 sm:-mt-20">
        <div role="tablist" aria-label="Movie view" className="mb-4 flex gap-2 px-4 sm:px-10">
          {([["category", "By Category"], ["all", "All Titles"]] as const).map(([key, label]) => (
            <button
              key={key}
              type="button"
              role="tab"
              aria-selected={view === key}
              onClick={() => setView(key)}
              className={`rounded-full px-4 py-1.5 text-sm font-medium transition ${
                view === key ? "bg-white text-black" : "bg-neutral-800/80 text-neutral-300 hover:bg-neutral-700"
              }`}
            >
              {label}
            </button>
          ))}
        </div>
        {view === "all" ? (
          <div className="grid grid-cols-3 gap-2 px-4 pb-24 pt-2 sm:grid-cols-4 sm:gap-3 sm:px-10 md:grid-cols-5 lg:grid-cols-6 xl:grid-cols-7 2xl:grid-cols-8">
            {allTitles.map((movie) => (
              <PosterCard key={movie.id} movie={movie} onSelect={setSelectedMovie} />
            ))}
          </div>
        ) : (
          <>
            <PosterRow title="Continue Processing" movies={inProgress} onSelect={setSelectedMovie} />
            <PosterRow title="Recently Added" movies={recentlyAdded} onSelect={setSelectedMovie} />
            <PosterRow title="Kids" movies={kids} onSelect={setSelectedMovie} />
            {byGenre.map(([label, list]) => (
              <PosterRow key={label} title={label} movies={list} onSelect={setSelectedMovie} />
            ))}
          </>
        )}
      </div>
    </div>
  );
}
