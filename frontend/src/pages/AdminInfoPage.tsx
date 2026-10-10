import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { apiFetch } from "../api/client";
import type { ServerInfo } from "../api/types";
import { PageLoader } from "../components/PageLoader";

type Tone = "good" | "warn" | "bad";
type Row = [label: string, value: ReactNode, tone?: Tone];

const toneStyle: Record<Tone, string> = {
  good: "text-emerald-300",
  warn: "text-amber-300",
  bad: "text-red-300",
};

function duration(seconds: number) {
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (days) return `${days}d ${hours}h`;
  if (hours) return `${hours}h ${minutes}m`;
  return `${minutes}m`;
}

function Meter({ used, total }: { used: number; total: number }) {
  const percent = total > 0 ? Math.max(0, Math.min(100, (used / total) * 100)) : 0;
  const color = percent >= 90 ? "bg-red-500" : percent >= 75 ? "bg-amber-400" : "bg-emerald-500";
  return (
    <div className="mt-1.5 h-1.5 w-full overflow-hidden rounded-full bg-neutral-700">
      <div className={`h-full ${color}`} style={{ width: `${percent}%` }} />
    </div>
  );
}

function Section({ title, rows }: { title: string; rows: Row[] }) {
  return (
    <div className="rounded bg-neutral-900 p-4 ring-1 ring-white/10">
      <h2 className="mb-3 text-sm font-semibold uppercase tracking-wider text-neutral-400">{title}</h2>
      <dl className="space-y-2 text-sm">
        {rows.map(([label, value, tone]) => (
          <div key={label} className="grid grid-cols-[6.5rem_minmax(0,1fr)] gap-3 sm:grid-cols-[9rem_minmax(0,1fr)]">
            <dt className="text-neutral-500">{label}</dt>
            <dd className={`min-w-0 break-words ${tone ? toneStyle[tone] : "text-neutral-100"}`}>{value ?? "Unknown"}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}

function tempTone(celsius: number): Tone {
  if (celsius >= 80) return "bad";
  if (celsius >= 70) return "warn";
  return "good";
}

export function AdminInfoPage() {
  const { data: info, isLoading, error } = useQuery({
    queryKey: ["server-info"],
    queryFn: () => apiFetch<ServerInfo>("/api/info"),
    refetchInterval: 5000,
  });

  if (isLoading) {
    return <PageLoader label="Loading server info" />;
  }

  const sys = info?.system;
  const lib = info?.library;
  const temp = sys?.cpu_temperature_celsius;
  const mem = sys?.memory_mb;
  const swap = sys?.swap_mb;

  const power: Row = !sys || sys.under_voltage == null
    ? ["Power", null]
    : sys.under_voltage
      ? ["Power", "Under-voltage now", "bad"]
      : sys.throttled
        ? ["Power", "CPU throttled", "warn"]
        : ["Power", "OK", "good"];

  return (
    <section className="mx-auto max-w-6xl px-4 py-10 sm:px-10">
      <div className="mb-8">
        <p className="mb-2 text-sm font-semibold uppercase tracking-[0.2em] text-[#e50914]">Admin</p>
        <h1 className="text-3xl font-bold text-white">Info</h1>
        <p className="mt-2 text-sm text-neutral-400">The media server's own health. Updates every 5 seconds.</p>
      </div>

      {error && <p className="mb-6 rounded bg-red-950 p-4 text-red-200">Could not load server info.</p>}

      {sys && lib && (
        <>
          <div className="mb-6 grid grid-cols-2 gap-3 sm:grid-cols-4">
            <div className="rounded bg-neutral-900 p-4 ring-1 ring-white/10">
              <div className={`break-words text-xl font-bold sm:text-2xl ${temp != null ? toneStyle[tempTone(temp)] : ""}`}>{temp != null ? `${temp.toFixed(1)}°C` : "—"}</div>
              <div className="text-sm text-neutral-400">CPU temperature</div>
            </div>
            <div className="rounded bg-neutral-900 p-4 ring-1 ring-white/10">
              <div className="break-words text-xl font-bold sm:text-2xl">{mem ? `${((mem.total - mem.available) / 1024).toFixed(1)} / ${(mem.total / 1024).toFixed(1)} GB` : "—"}</div>
              <div className="text-sm text-neutral-400">Memory used</div>
              {mem && <Meter used={mem.total - mem.available} total={mem.total} />}
            </div>
            <div className="rounded bg-neutral-900 p-4 ring-1 ring-white/10">
              <div className="break-words text-xl font-bold sm:text-2xl">{sys.disks[0] ? `${sys.disks[0].free} GB` : "—"}</div>
              <div className="text-sm text-neutral-400">{sys.disks[0] ? `Free · ${sys.disks[0].label.toLowerCase()}` : "Free space"}</div>
              {sys.disks[0] && <Meter used={sys.disks[0].total - sys.disks[0].free} total={sys.disks[0].total} />}
            </div>
            <div className="rounded bg-neutral-900 p-4 ring-1 ring-white/10">
              <div className="break-words text-xl font-bold sm:text-2xl">{sys.uptime_seconds != null ? duration(sys.uptime_seconds) : "—"}</div>
              <div className="text-sm text-neutral-400">Uptime</div>
            </div>
          </div>

          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
            <Section
              title="Device"
              rows={[
                ["Model", sys.model],
                ["OS", sys.os],
                ["Kernel", sys.kernel],
                ["CPU", `${sys.cpu_count} cores · ${sys.arch}`],
                ["Temperature", temp != null ? `${temp.toFixed(1)}°C` : null, temp != null ? tempTone(temp) : undefined],
                power,
                ["Load average", sys.load_average ? sys.load_average.map((n) => n.toFixed(2)).join(" · ") : null],
                ["Uptime", sys.uptime_seconds != null ? duration(sys.uptime_seconds) : null],
              ]}
            />
            <Section
              title="Network"
              rows={[
                ["Hostname", sys.hostname],
                ["Address on network", sys.mdns_name],
                ["IP address", sys.addresses.length ? sys.addresses.map((a) => `${a.address} (${a.interface})`).join(", ") : null],
              ]}
            />
            <Section
              title="Memory and storage"
              rows={[
                ["Memory", mem ? <>{mem.total - mem.available} / {mem.total} MB used<Meter used={mem.total - mem.available} total={mem.total} /></> : null],
                ...(swap ? [["Swap", `${swap.total - swap.available} / ${swap.total} MB used`] as Row] : []),
                ...sys.disks.map((d): Row => [
                  d.label,
                  <>
                    {d.free} GB free of {d.total} GB
                    <span className="block font-mono text-xs text-neutral-500">{d.path}</span>
                    <Meter used={d.total - d.free} total={d.total} />
                  </>,
                  d.total > 0 && d.free / d.total < 0.1 ? "bad" : undefined,
                ]),
              ]}
            />
            <Section
              title="Library and software"
              rows={[
                ["Movies", lib.movies],
                ["Music tracks", lib.tracks],
                ["Players checked in", lib.devices],
                ["Active jobs", lib.jobs_active],
                ["Server running for", duration(sys.server_uptime_seconds)],
                ["Version", sys.software.commit ? `${sys.software.commit}${sys.software.modified ? " (modified)" : ""}` : null],
                ["Latest change date", sys.software.date ? new Date(sys.software.date).toLocaleString() : null],
                ["Go", sys.software.go_version],
              ]}
            />
          </div>
        </>
      )}
    </section>
  );
}
