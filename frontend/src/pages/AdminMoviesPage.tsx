import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { movieImageUrl } from "../api/client";
import { useDeleteMovie, useMovies, useRenameMovie } from "../hooks/useMovies";
import type { Movie } from "../api/types";
import { MatchPicker } from "../components/MatchPicker";
import { PageLoader } from "../components/PageLoader";

function AdminMovieRow({ movie }: { movie: Movie }) {
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(movie.title);
  const [showTMDB, setShowTMDB] = useState(false);
  const rename = useRenameMovie(movie.id);
  const remove = useDeleteMovie();

  const save = async () => {
    const next = title.trim();
    if (!next) return;
    await rename.mutateAsync(next);
    setEditing(false);
  };

  return (
    <li className="grid grid-cols-[64px_minmax(0,1fr)] items-center gap-x-4 gap-y-3 border-b border-white/10 px-4 py-3 transition hover:bg-white/[.04] sm:grid-cols-[72px_1fr_110px_270px] sm:px-6">
      <Link to={`/movies/${movie.id}`} className="h-14 w-16 overflow-hidden rounded bg-neutral-800 sm:h-16 sm:w-[72px]">
        {movie.hasPoster && <img src={movieImageUrl(movie.id, "primary", movie.posterTag)} alt="" className="h-full w-full object-cover" />}
      </Link>
      <div className="min-w-0">
        {editing ? (
          <form className="flex max-w-xl gap-2" onSubmit={(e) => { e.preventDefault(); void save(); }}>
            <input autoFocus maxLength={200} value={title} onChange={(e) => setTitle(e.target.value)} className="min-w-0 flex-1 rounded border border-white/30 bg-black px-3 py-2 text-white outline-none focus:border-white" />
            <button className="rounded bg-white px-3 py-2 text-sm font-bold text-black">Save</button>
          </form>
        ) : (
          <>
            <Link to={`/movies/${movie.id}`} className="block truncate font-semibold text-white hover:underline">{movie.title}</Link>
            <p className="mt-1 truncate text-xs text-neutral-500">{movie.originalFilename}</p>
          </>
        )}
      </div>
      <span className="hidden text-sm capitalize text-neutral-400 sm:block">{movie.status.replace(/_/g, " ")}</span>
      <div className="col-start-2 flex flex-wrap gap-2 sm:col-start-auto sm:justify-end">
        <button onClick={() => setShowTMDB(true)} className="rounded border border-[#e50914]/70 px-3 py-2 text-sm text-red-100 hover:border-[#e50914] hover:bg-[#e50914]/15">TMDB</button>
        <button onClick={() => { setTitle(movie.title); setEditing((v) => !v); }} className="rounded border border-white/20 px-3 py-2 text-sm hover:border-white">Rename</button>
        <button
          onClick={() => {
            if (window.confirm(`Delete “${movie.title}” and its media file?`)) remove.mutate(movie.id);
          }}
          disabled={remove.isPending}
          className="rounded bg-red-700 px-3 py-2 text-sm font-semibold hover:bg-red-600 disabled:opacity-50"
        >Delete</button>
      </div>
      {showTMDB && (
        <MatchPicker
          movieId={movie.id}
          initialQuery={movie.title}
          initialYear={movie.year || undefined}
          onClose={() => setShowTMDB(false)}
        />
      )}
    </li>
  );
}

export function AdminMoviesPage() {
  const { data: movies = [], isLoading } = useMovies();
  const [query, setQuery] = useState("");
  const filtered = useMemo(() => movies.filter((m) => `${m.title} ${m.originalFilename}`.toLowerCase().includes(query.toLowerCase())), [movies, query]);

  if (isLoading) {
    return <PageLoader label="Loading movie library" />;
  }

  return (
    <div className="mx-auto max-w-6xl px-4 py-10 sm:px-10">
      <div className="mb-8 flex flex-col justify-between gap-4 sm:flex-row sm:items-end">
        <div><p className="text-xs font-bold uppercase tracking-[.22em] text-[#e50914]">Library management</p><h1 className="mt-2 text-4xl font-black">Movies</h1><p className="mt-2 text-neutral-400">{movies.length} titles in your library</p></div>
        <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Search movies…" className="w-full rounded bg-white/10 px-4 py-3 outline-none ring-1 ring-white/10 focus:ring-white sm:w-72" />
      </div>
      <div className="overflow-hidden rounded-lg bg-[#1b1b1b] shadow-2xl ring-1 ring-white/10">
        <ul>{filtered.map((movie) => <AdminMovieRow key={movie.id} movie={movie} />)}</ul>
      </div>
    </div>
  );
}
