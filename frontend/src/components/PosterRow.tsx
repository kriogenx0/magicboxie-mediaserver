import { useRef } from "react";
import type { Movie } from "../api/types";
import { PosterCard } from "./PosterCard";

export function PosterRow({ title, movies, onSelect }: { title: string; movies: Movie[]; onSelect?: (movie: Movie) => void }) {
  const scrollerRef = useRef<HTMLDivElement>(null);

  if (movies.length === 0) return null;

  const scrollByPage = (direction: 1 | -1) => {
    const el = scrollerRef.current;
    if (!el) return;
    el.scrollBy({ left: direction * el.clientWidth * 0.9, behavior: "smooth" });
  };

  return (
    <section className="group/row relative mb-9 sm:mb-12">
      <h2 className="mb-3 px-4 text-lg font-bold tracking-tight text-white sm:px-10 sm:text-xl">{title}</h2>

      <button
        type="button"
        aria-label="Scroll left"
        onClick={() => scrollByPage(-1)}
        className="absolute bottom-0 left-0 top-6 z-10 hidden w-10 items-center justify-center bg-gradient-to-r from-[#141414] to-transparent text-3xl text-white opacity-0 transition duration-200 hover:opacity-100 focus:opacity-100 group-hover/row:opacity-100 sm:flex sm:w-14"
      >
        ‹
      </button>

      <div ref={scrollerRef} className="scroll-row flex gap-2 overflow-x-auto px-4 pb-24 pt-12 sm:gap-2.5 sm:px-10">
        {movies.map((movie) => (
          <div key={movie.id} className="w-28 shrink-0 sm:w-36 md:w-40 lg:w-44 xl:w-48">
            <PosterCard movie={movie} onSelect={onSelect} />
          </div>
        ))}
      </div>

      <button
        type="button"
        aria-label="Scroll right"
        onClick={() => scrollByPage(1)}
        className="absolute bottom-0 right-0 top-6 z-10 hidden w-10 items-center justify-center bg-gradient-to-l from-[#141414] to-transparent text-3xl text-white opacity-0 transition duration-200 hover:opacity-100 focus:opacity-100 group-hover/row:opacity-100 sm:flex sm:w-14"
      >
        ›
      </button>
    </section>
  );
}
