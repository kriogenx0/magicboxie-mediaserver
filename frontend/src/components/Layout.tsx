import { useEffect, useState, type ReactNode } from "react";
import { Link, useLocation } from "react-router-dom";
import { useAuth } from "../hooks/useAuth";
import { useUploadQueue } from "../hooks/useUploadQueue";
import { UploadDropzone } from "./UploadDropzone";

export function Layout({ children }: { children: ReactNode }) {
  const [showUpload, setShowUpload] = useState(false);
  const { logout } = useAuth();
  const location = useLocation();
  const onMusic = location.pathname.startsWith("/music");
  const onAdmin = location.pathname.startsWith("/admin");
  const hasHero = location.pathname === "/" || location.pathname.startsWith("/movies/");
  const { addFiles } = useUploadQueue();

  const [isDragActive, setIsDragActive] = useState(false);

  // Listen on window so a file dropped over a child control, image, or media
  // element cannot bypass the upload target. Calling preventDefault during
  // dragover is also required for browsers to emit the final drop event.
  useEffect(() => {
    const containsFiles = (event: DragEvent) =>
      Array.from(event.dataTransfer?.types ?? []).includes("Files");

    const handleDragOver = (event: DragEvent) => {
      if (!containsFiles(event)) return;
      event.preventDefault();
      if (event.dataTransfer) event.dataTransfer.dropEffect = "copy";
      setIsDragActive(true);
    };
    const handleDragLeave = (event: DragEvent) => {
      if (!event.relatedTarget) setIsDragActive(false);
    };
    const handleDrop = (event: DragEvent) => {
      if (!containsFiles(event)) return;
      event.preventDefault();
      setIsDragActive(false);
      const files = Array.from(event.dataTransfer?.files ?? []);
      if (files.length === 0) return;
      addFiles(files);
      setShowUpload(true);
    };

    window.addEventListener("dragover", handleDragOver);
    window.addEventListener("dragleave", handleDragLeave);
    window.addEventListener("drop", handleDrop);
    return () => {
      window.removeEventListener("dragover", handleDragOver);
      window.removeEventListener("dragleave", handleDragLeave);
      window.removeEventListener("drop", handleDrop);
    };
  }, [addFiles]);

  return (
    <div className="app-surface min-h-screen pb-24">
      {isDragActive && (
        <div className="pointer-events-none fixed inset-0 z-50 flex items-center justify-center border-4 border-dashed border-red-600 bg-black/80">
          <p className="text-2xl font-semibold text-white">Drop to upload</p>
        </div>
      )}
      <header
        className={`${hasHero ? "absolute" : "sticky"} top-0 z-20 flex w-full flex-wrap items-center justify-between gap-x-3 gap-y-2 bg-gradient-to-b from-black via-black/85 to-transparent px-4 py-3 sm:h-[68px] sm:flex-nowrap sm:px-10 sm:py-0`}
      >
        <div className="contents sm:flex sm:min-w-0 sm:items-center sm:gap-7">
          <Link to="/" className="netflix-logo shrink-0 text-xl font-black uppercase text-[#e50914] sm:text-2xl" aria-label="MagicBoxie">
            MagicBoxie
          </Link>
          <nav className="scroll-row order-last flex w-full min-w-0 items-center gap-5 overflow-x-auto whitespace-nowrap text-sm font-medium sm:order-none sm:w-auto">
            <Link
              to="/"
              className={!onMusic && !onAdmin ? "text-white" : "text-neutral-400 transition hover:text-neutral-200"}
            >
              Movies
            </Link>
            <Link
              to="/music"
              className={onMusic ? "text-white" : "text-neutral-400 transition hover:text-neutral-200"}
            >
              Music
            </Link>
            <Link to="/admin/movies" className={onAdmin ? "text-white" : "text-neutral-400 transition hover:text-neutral-200"}>
              Admin
            </Link>
            {onAdmin && (
              <Link to="/admin/activity" className={location.pathname === "/admin/activity" ? "text-white" : "text-neutral-400 transition hover:text-neutral-200"}>
                Activity
              </Link>
            )}
            {onAdmin && (
              <Link to="/admin/devices" className={location.pathname === "/admin/devices" ? "text-white" : "text-neutral-400 transition hover:text-neutral-200"}>
                Devices
              </Link>
            )}
          </nav>
        </div>
        <div className="flex shrink-0 items-center gap-2 sm:gap-3">
          <button
            onClick={() => setShowUpload((v) => !v)}
            className="whitespace-nowrap rounded-sm border border-neutral-500 px-2.5 py-1 text-xs font-medium text-neutral-100 transition hover:border-white hover:bg-white/10 sm:px-3 sm:text-sm"
          >
            {showUpload ? "Close" : "Add Media"}
          </button>
          <button
            onClick={logout}
            className="hidden rounded-sm px-2 py-1 text-xs text-neutral-400 transition hover:text-white sm:block"
          >
            Log out
          </button>
        </div>
      </header>

      {showUpload && (
        <div className={`mb-4 ${hasHero ? "pt-16 sm:pt-[68px]" : ""}`}>
          <UploadDropzone />
        </div>
      )}

      <main>{children}</main>
    </div>
  );
}
