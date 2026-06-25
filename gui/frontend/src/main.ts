import "./style.css";
import {
  GetEnv, Config, Health, ServerRunning,
  StartServer, StopServer, BuildGraph, BuildGeocode, CreateToken,
} from "../wailsjs/go/main/App";
import { EventsOn, BrowserOpenURL } from "../wailsjs/runtime/runtime";

const $ = <T extends HTMLElement = HTMLElement>(id: string) => document.getElementById(id) as T;

let port = 8443;
let building = false;

// ---------- Logging ----------
const logEl = $<HTMLPreElement>("logOutput");
function log(line: string) {
  logEl.textContent += line + "\n";
  logEl.scrollTop = logEl.scrollHeight;
}
$("btnClearLog").onclick = () => (logEl.textContent = "");

// ---------- Status-Helfer ----------
const GREEN = "bg-green", RED = "bg-red", FAINT = "bg-faint";
function setDot(id: string, cls: string) {
  const el = $(id);
  el.classList.remove(GREEN, RED, FAINT);
  el.classList.add(cls);
}
function setChip(online: boolean) {
  setDot("statusDot", online ? GREEN : FAINT);
  $("statusText").textContent = online ? "Server online" : "Server offline";
}

// ---------- Health-Poll ----------
async function refreshHealth() {
  const h = await Health(port);
  setChip(h.online);
  setDot("engineDot", h.online ? (h.engineReady ? GREEN : RED) : FAINT);
  $("engineText").textContent = h.online ? (h.engineReady ? "bereit" : "kein Graph") : "—";
  setDot("geocodeDot", h.online ? (h.geocodeReady ? GREEN : RED) : FAINT);
  $("geocodeText").textContent = h.online ? (h.geocodeReady ? "geladen" : "keine Tabelle") : "—";
  $("authText").textContent = h.online ? (h.authRequired ? "aktiv" : "offen") : "—";
}

// ---------- Server-Status (Prozess) ----------
function setServerRunning(running: boolean) {
  $("btnStart").classList.toggle("hidden", running);
  $("btnStop").classList.toggle("hidden", !running);
}

// ---------- Konfiguration ----------
const CONFIG_LABELS: Record<string, string> = {
  osrm_graph_path: "Graph-Pfad",
  osrm_routed_url: "osrm-routed",
  geocode_path: "Geocoding-CSV",
  graph_present: "Graph vorhanden",
  geocode_present: "Tabelle vorhanden",
  workers: "Worker",
  snap_limit_m: "Snap-Limit (m)",
  auth_enabled: "Auth aktiv",
  addin_port: "Port",
};
async function refreshConfig() {
  try {
    const c = await Config();
    port = Number(c["addin_port"]) || 8443;
    const tbody = $("configTable");
    tbody.innerHTML = "";
    for (const [key, label] of Object.entries(CONFIG_LABELS)) {
      if (!(key in c)) continue;
      const tr = document.createElement("tr");
      tr.innerHTML =
        `<td class="py-1 text-muted">${label}</td>` +
        `<td class="py-1 text-right font-mono">${fmt(c[key])}</td>`;
      tbody.appendChild(tr);
    }
    $("graphState").textContent = c["graph_present"] ? "vorhanden" : "fehlt — bitte bauen";
    $("geocodeFileState").textContent = c["geocode_present"] ? "vorhanden" : "fehlt — bitte bauen";
  } catch (e) {
    log("Konfiguration nicht lesbar: " + e);
  }
}
function fmt(v: unknown): string {
  if (v === true) return "ja";
  if (v === false) return "nein";
  return String(v);
}

// ---------- Aktionen ----------
$("btnStart").onclick = async () => {
  try { await StartServer(); } catch (e) { log("Start fehlgeschlagen: " + e); }
};
$("btnStop").onclick = async () => { await StopServer(); };
$("btnOpen").onclick = () => BrowserOpenURL(`https://127.0.0.1:${port}/addin/taskpane.html`);

function setBuilding(b: boolean) {
  building = b;
  ($("btnGraph") as HTMLButtonElement).disabled = b;
  ($("btnGeocode") as HTMLButtonElement).disabled = b;
}
$("btnGraph").onclick = async () => {
  if (building) return;
  setBuilding(true);
  log("\n=== OSRM-Graph bauen ===");
  try { await BuildGraph(); } catch (e) { log("Fehler: " + e); setBuilding(false); }
};
$("btnGeocode").onclick = async () => {
  if (building) return;
  setBuilding(true);
  const country = ($("countryInput") as HTMLInputElement).value.trim().toUpperCase() || "DE";
  log(`\n=== Geocoding-Tabelle bauen (${country}) ===`);
  try { await BuildGeocode(country); } catch (e) { log("Fehler: " + e); setBuilding(false); }
};

$("btnToken").onclick = async () => {
  const name = ($("tokenName") as HTMLInputElement).value.trim();
  const days = Number(($("tokenDays") as HTMLInputElement).value) || 90;
  $("tokenErr").classList.add("hidden");
  try {
    const tok = await CreateToken(name, days);
    ($("tokenOut") as HTMLInputElement).value = tok;
    $("tokenWrap").classList.remove("hidden");
  } catch (e) {
    const el = $("tokenErr");
    el.textContent = String(e);
    el.classList.remove("hidden");
  }
};
$("btnCopyToken").onclick = () => {
  const inp = $("tokenOut") as HTMLInputElement;
  navigator.clipboard?.writeText(inp.value);
  inp.select();
};

// ---------- Wails-Events ----------
EventsOn("server:log", (line: string) => log(line));
EventsOn("server:started", () => { setServerRunning(true); log("Server gestartet."); });
EventsOn("server:stopped", () => { setServerRunning(false); log("Server gestoppt."); refreshHealth(); });
EventsOn("build:log", (line: string) => log(line));
EventsOn("build:done", (r: { ok: boolean; error?: string }) => {
  setBuilding(false);
  log(r.ok ? "✓ fertig." : "✗ fehlgeschlagen: " + r.error);
  refreshConfig();
  refreshHealth();
});

// ---------- Init ----------
async function init() {
  const env = await GetEnv();
  if (!env.binOK) {
    $("binWarn").classList.remove("hidden");
    $("binPath").textContent = env.binary;
  }
  setServerRunning(await ServerRunning());
  await refreshConfig();
  await refreshHealth();
  setInterval(refreshHealth, 4000);
}
init();
