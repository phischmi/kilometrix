/* Kilometrix Office.js Add-in — liest Koordinaten, ruft das Backend
   (/route-batch, same-origin) und schreibt Ergebnisse ins Blatt zurück.
   Bei tokengeschütztem Server: Token-Gate + Bearer-Auth. */

(() => {
  "use strict";

  const API = ""; // same-origin: das Backend liefert dieses Add-in selbst aus
  // Zwei Eingabemodi: "route" = fertige Koordinaten, "geo" = LKZ/PLZ → Zentroid (serverseitig).
  const COORD_TARGETS = ["origin_lat", "origin_lon", "dest_lat", "dest_lon"];
  const GEO_TARGETS = ["origin_lkz", "origin_plz", "dest_lkz", "dest_plz"];
  const ALL_TARGETS = [...COORD_TARGETS, ...GEO_TARGETS];
  const targets = () => (settings.mode === "geo" ? GEO_TARGETS : COORD_TARGETS);
  // Blockgröße fürs Streaming: pro Block wird gelesen → berechnet → zurückgeschrieben.
  // Hält den Speicher konstant und macht das Add-in auch für sehr große Blätter tauglich.
  const BLOCK = 2000;
  const TOKEN_KEY = "kmx_token";
  const SETTINGS_KEY = "kmx_settings";

  const $ = (id) => document.getElementById(id);
  const state = { scope: "used", engineReady: false, authRequired: false, authed: false, ctx: null };
  const settings = {
    cols: { distance: true, duration: true, status: true, snap: false },
    durFormat: "min", // "min" | "hhmm"
    mode: "geo", // Default: "geo" (LKZ/PLZ → Zentroid) | "route" (fertige Koordinaten)
  };

  let officeInitialized = false;

  Office.onReady((info) => {
    officeInitialized = true;
    // Außerhalb von Excel (z. B. direkt im Browser) hat das Add-in keine Funktion —
    // statt des UIs nur den Hinweis zeigen.
    if (info.host !== Office.HostType.Excel) {
      showNotExcel();
      return;
    }
    showApp();
    showVersion();
    $("backendUrl").textContent = location.host;
    applyTheme();
    watchTheme();
    loadSettings();
    wireEvents();
    boot();

    // bei Auswahländerung im Markierungs-Modus live aktualisieren
    Excel.run(async (ctx) => {
      ctx.workbook.worksheets.getActiveWorksheet().onSelectionChanged.add(async () => {
        if (state.authed && state.scope === "selection") refreshContext();
      });
      await ctx.sync();
    }).catch(() => {});
  });

  // Fallback: Wird die Seite außerhalb von Office geöffnet, initialisiert office.js u. U.
  // gar nicht (z. B. im Browser ohne Internet) → onReady feuert nie. Nach kurzer Wartezeit
  // den Hinweis zeigen, statt das (funktionslose) UI stehen zu lassen.
  setTimeout(() => { if (!officeInitialized) showNotExcel(); }, 3000);

  function showApp() { $("notExcel").hidden = true; $("appRoot").hidden = false; }
  function showNotExcel() { $("appRoot").hidden = true; $("notExcel").hidden = false; }

  // Add-in-Version aus dem Manifest (Single Source) lesen und im Footer zeigen.
  async function showVersion() {
    try {
      const xml = await (await fetch("manifest.xml")).text();
      const doc = new DOMParser().parseFromString(xml, "application/xml");
      const v = doc.getElementsByTagNameNS("*", "Version")[0]?.textContent?.trim();
      if (v) $("appVersion").textContent = ` · v${v}`;
    } catch {}
  }

  // Reihenfolge: Status prüfen → ggf. Token-Gate → Tabellenkontext laden
  async function boot() {
    await checkHealth();
    if (!state.authRequired) {
      state.authed = true;
    } else {
      const t = getToken();
      state.authed = t ? await checkToken(t) : false;
    }
    setGate(!state.authed);
    if (state.authed) {
      $("settingsBtn").hidden = false;
      await refreshContext();
    }
  }

  function wireEvents() {
    $("segUsed").onclick = () => setScope("used");
    $("segSel").onclick = () => setScope("selection");
    $("hasHeader").onchange = refreshContext;
    ALL_TARGETS.forEach((t) => ($(t).onchange = updateRunState));
    $("modeRoute").onclick = () => setMode("route");
    $("modeGeo").onclick = () => setMode("geo");
    $("runBtn").onclick = run;
    $("tokenSave").onclick = onTokenSave;
    $("tokenInput").addEventListener("keydown", (e) => { if (e.key === "Enter") onTokenSave(); });
    $("settingsBtn").onclick = openSettings;
    $("settingsDone").onclick = closeSettings;
    $("durMin").onclick = () => setDurFormat("min");
    $("durHhmm").onclick = () => setDurFormat("hhmm");
    ["col_distance", "col_duration", "col_status", "col_snap"].forEach((id) => ($(id).onchange = readColsFromUI));
  }

  // ---------- Einstellungen ----------
  function loadSettings() {
    try {
      const s = JSON.parse(localStorage.getItem(SETTINGS_KEY) || "null");
      if (s && s.cols) {
        Object.assign(settings.cols, s.cols);
        settings.durFormat = s.durFormat === "hhmm" ? "hhmm" : "min";
        settings.mode = s.mode === "route" ? "route" : "geo"; // Default geo, nur explizit route respektieren
      }
    } catch {}
    $("col_distance").checked = settings.cols.distance;
    $("col_duration").checked = settings.cols.duration;
    $("col_status").checked = settings.cols.status;
    $("col_snap").checked = settings.cols.snap;
    setDurFormat(settings.durFormat, false);
    setMode(settings.mode, false);
  }
  const saveSettings = () => { try { localStorage.setItem(SETTINGS_KEY, JSON.stringify(settings)); } catch {} };

  function readColsFromUI() {
    settings.cols.distance = $("col_distance").checked;
    settings.cols.duration = $("col_duration").checked;
    settings.cols.status = $("col_status").checked;
    settings.cols.snap = $("col_snap").checked;
    saveSettings();
    updateRunState();
  }
  function setDurFormat(fmt, persist = true) {
    settings.durFormat = fmt === "hhmm" ? "hhmm" : "min";
    $("durMin").setAttribute("aria-pressed", settings.durFormat === "min");
    $("durHhmm").setAttribute("aria-pressed", settings.durFormat === "hhmm");
    if (persist) saveSettings();
  }
  // Eingabemodus umschalten: Koordinaten („route") vs. LKZ/PLZ („geo").
  function setMode(mode, persist = true) {
    settings.mode = mode === "geo" ? "geo" : "route";
    $("modeRoute").setAttribute("aria-pressed", settings.mode === "route");
    $("modeGeo").setAttribute("aria-pressed", settings.mode === "geo");
    $("grid_coords").hidden = settings.mode !== "route";
    $("grid_plz").hidden = settings.mode !== "geo";
    $("modeHint").hidden = settings.mode !== "geo";
    if (persist) saveSettings();
    if (state.ctx) fillSelects(state.ctx.headers);
    updateRunState();
  }

  function openSettings() { $("settings").hidden = false; $("main").style.display = "none"; }
  function closeSettings() { $("settings").hidden = true; if (state.authed) $("main").style.display = ""; }

  // Welche Ergebnis-Spalten geschrieben werden (Reihenfolge + Header + Formatierung)
  function outputSpec() {
    const spec = [];
    // Im Geocoding-Modus die hergeleiteten Koordinaten sichtbar voranstellen.
    if (settings.mode === "geo") {
      spec.push({ header: "origin_lat", val: (r) => numOrBlank(r.origin_lat) });
      spec.push({ header: "origin_lon", val: (r) => numOrBlank(r.origin_lon) });
      spec.push({ header: "dest_lat", val: (r) => numOrBlank(r.dest_lat) });
      spec.push({ header: "dest_lon", val: (r) => numOrBlank(r.dest_lon) });
    }
    if (settings.cols.distance) spec.push({ header: "distance_km", val: (r) => numOrBlank(r.distance_km) });
    if (settings.cols.duration) {
      if (settings.durFormat === "hhmm") spec.push({ header: "duration_hhmm", val: (r) => hhmm(r.duration_min) });
      else spec.push({ header: "duration_min", val: (r) => numOrBlank(r.duration_min) });
    }
    if (settings.cols.status) spec.push({ header: "status", val: (r) => r.status });
    if (settings.cols.snap) spec.push({ header: "snap_m", val: (r) => numOrBlank(r.snap_m) });
    return spec;
  }
  function hhmm(min) {
    if (min == null) return "";
    const total = Math.round(min);
    return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, "0")}`;
  }

  // ---------- Theme (folgt dem Office-/System-Dark-Mode) ----------
  function isDarkColor(hex) {
    if (!hex) return false;
    const c = String(hex).replace("#", "");
    const h = c.length >= 6 ? c.slice(-6) : c;
    const r = parseInt(h.slice(0, 2), 16), g = parseInt(h.slice(2, 4), 16), b = parseInt(h.slice(4, 6), 16);
    if ([r, g, b].some(Number.isNaN)) return false;
    return 0.299 * r + 0.587 * g + 0.114 * b < 128; // wahrgenommene Helligkeit
  }
  function applyTheme() {
    let dark = null;
    try {
      const t = Office.context && Office.context.officeTheme;
      if (t && t.bodyBackgroundColor) dark = isDarkColor(t.bodyBackgroundColor);
    } catch {}
    if (dark === null) document.documentElement.removeAttribute("data-theme"); // OS-Fallback (CSS)
    else document.documentElement.dataset.theme = dark ? "dark" : "light";
  }
  function watchTheme() {
    try { window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", applyTheme); } catch {}
    // Office-Theme-Wechsel greifen, sobald das Pane wieder sichtbar wird
    document.addEventListener("visibilitychange", () => { if (!document.hidden) applyTheme(); });
  }

  // ---------- Token / Zugang ----------
  const getToken = () => { try { return localStorage.getItem(TOKEN_KEY) || ""; } catch { return ""; } };
  const setTok = (t) => { try { localStorage.setItem(TOKEN_KEY, t); } catch {} };
  const authHeaders = () => { const t = getToken(); return t ? { Authorization: `Bearer ${t}` } : {}; };

  function setGate(locked) {
    $("gate").hidden = !locked;
    $("main").style.display = locked ? "none" : "";
  }

  async function checkToken(token) {
    try {
      const r = await fetch(`${API}/auth/check`, { headers: { Authorization: `Bearer ${token}` } });
      if (!r.ok) return false;
      const d = await r.json();
      state.tokenName = d.name;
      if (d.name) $("backendUrl").textContent = `${location.host} · ${d.name}`;
      return true;
    } catch { return false; }
  }

  async function onTokenSave() {
    $("gateAlert").classList.remove("is-on");
    const t = $("tokenInput").value.trim();
    if (!t) return;
    if (await checkToken(t)) {
      setTok(t);
      state.authed = true;
      setGate(false);
      await refreshContext();
    } else {
      $("gateAlertText").textContent = "Token ungültig oder abgelaufen.";
      $("gateAlert").classList.add("is-on");
    }
  }

  function setScope(scope) {
    state.scope = scope;
    $("segUsed").setAttribute("aria-pressed", scope === "used");
    $("segSel").setAttribute("aria-pressed", scope === "selection");
    refreshContext();
  }

  // ---------- Backend-Status ----------
  async function checkHealth() {
    try {
      const h = await (await fetch(`${API}/health`)).json();
      state.engineReady = !!h.engine_ready;
      state.authRequired = !!h.auth_required;
      const el = $("status");
      if (state.engineReady) {
        el.className = "status status--ready";
        $("statusText").textContent = "bereit";
      } else {
        el.className = "status status--off";
        $("statusText").textContent = "kein Graph";
      }
    } catch {
      state.engineReady = false;
      $("status").className = "status status--off";
      $("statusText").textContent = "offline";
    }
    updateRunState();
  }

  // ---------- Tabellen-Kontext lesen (leichtgewichtig) ----------
  async function refreshContext() {
    try {
      await Excel.run(async (ctx) => {
        const sheet = ctx.workbook.worksheets.getActiveWorksheet();
        const range =
          state.scope === "selection"
            ? ctx.workbook.getSelectedRange()
            : sheet.getUsedRange();
        range.load("rowIndex,columnIndex,rowCount,columnCount,address");
        await ctx.sync();

        const headerRow = range.getRow(0);
        headerRow.load("values");
        await ctx.sync();

        const hasHeader = $("hasHeader").checked;
        const colCount = range.columnCount;
        const firstRow = headerRow.values[0];
        const headers = [];
        for (let c = 0; c < colCount; c++) {
          const letter = colLetter(range.columnIndex + c);
          const name = hasHeader ? String(firstRow[c] ?? "").trim() : "";
          headers.push({ idx: c, letter, label: name ? `${letter} · ${name}` : letter, name });
        }

        state.ctx = {
          rowIndex: range.rowIndex,
          columnIndex: range.columnIndex,
          rowCount: range.rowCount,
          columnCount: colCount,
          address: range.address,
          headers,
          hasHeader,
          dataRows: Math.max(0, range.rowCount - (hasHeader ? 1 : 0)),
        };

        $("rangeAddr").textContent = (range.address || "").split("!").pop() || "—";
        $("rowCount").textContent = state.ctx.dataRows.toLocaleString("de-DE");
        fillSelects(headers);
        updateRunState();
      });
    } catch (e) {
      $("rangeAddr").textContent = "—";
      $("rowCount").textContent = "0";
    }
  }

  function fillSelects(headers) {
    const guess = suggest(headers);
    targets().forEach((t) => {
      const sel = $(t);
      const prev = sel.value;
      sel.innerHTML = "";
      headers.forEach((h) => {
        const o = document.createElement("option");
        o.value = String(h.idx);
        o.textContent = h.label;
        sel.appendChild(o);
      });
      const want = prev !== "" && headers.some((h) => String(h.idx) === prev) ? prev : guess[t];
      if (want != null) sel.value = String(want);
    });
  }

  function suggest(headers) {
    const find = (re) => {
      const h = headers.find((x) => re.test(x.name.toLowerCase()));
      return h ? h.idx : null;
    };
    return {
      origin_lat: find(/^(origin|start|von).*(lat|breite|y)|^lat|breite/),
      origin_lon: find(/^(origin|start|von).*(lon|lng|länge|x)|^lon|länge/),
      dest_lat: find(/^(dest|ziel|nach|to).*(lat|breite|y)/),
      dest_lon: find(/^(dest|ziel|nach|to).*(lon|lng|länge|x)/),
      origin_lkz: find(/^(origin|start|von|abs).*(lkz|land|country|iso|nation)|^(lkz|land|country|iso|nation)/),
      origin_plz: find(/^(origin|start|von|abs).*(plz|postleit|zip)|^(plz|postleit|zip)/),
      dest_lkz: find(/^(dest|ziel|nach|to|emp).*(lkz|land|country|iso|nation)/),
      dest_plz: find(/^(dest|ziel|nach|to|emp).*(plz|postleit|zip)/),
    };
  }

  function updateRunState() {
    const c = state.ctx;
    // Im Geocoding-Modus entstehen immer Koordinatenspalten → es gibt stets eine Ausgabe.
    const anyCol =
      settings.mode === "geo" ||
      settings.cols.distance || settings.cols.duration || settings.cols.status || settings.cols.snap;
    const ready =
      state.engineReady && c && c.dataRows > 0 && anyCol && targets().every((t) => $(t).value !== "");
    $("runBtn").disabled = !ready;
  }

  // ---------- Berechnung (Streaming in Blöcken) ----------
  async function run() {
    hideAlert();
    $("results").classList.remove("is-on");
    const c = state.ctx;
    if (!c) return;
    const geo = settings.mode === "geo";
    const map = {};
    targets().forEach((t) => (map[t] = parseInt($(t).value, 10)));

    setRunning(true);
    const t0 = performance.now();
    const startCol = c.columnIndex + c.columnCount;
    const counts = { total: c.dataRows, ok: 0, far: 0, bad: 0 };
    const n = c.dataRows;
    const spec = outputSpec(); // welche Spalten + Format

    try {
      await writeHeader(c, startCol, spec); // Überschriften einmalig

      let done = 0;
      setProgress(0, n);
      for (let start = 0; start < n; start += BLOCK) {
        const len = Math.min(BLOCK, n - start);
        const cols = await readBlock(c, map, start, len);

        // lokale Validierung: ungültige Zeilen markieren, gültige senden
        const block = new Array(len);
        const send = [];
        const sendIdx = [];
        for (let i = 0; i < len; i++) {
          if (geo) {
            // Geocoding-Modus: LKZ/PLZ je Endpunkt; Auflösung passiert serverseitig.
            const oLkz = str(cols.origin_lkz[i]), oPlz = str(cols.origin_plz[i]);
            const dLkz = str(cols.dest_lkz[i]), dPlz = str(cols.dest_plz[i]);
            if (!oLkz || !oPlz || !dLkz || !dPlz) {
              block[i] = blank("error");
            } else {
              sendIdx.push(i);
              // Datenminimierung: nur LKZ/PLZ verlassen das Blatt, keine IDs/weiteren Spalten.
              send.push({ origin_lkz: oLkz, origin_plz: oPlz, dest_lkz: dLkz, dest_plz: dPlz });
            }
          } else {
            const oLa = num(cols.origin_lat[i]), oLo = num(cols.origin_lon[i]);
            const dLa = num(cols.dest_lat[i]), dLo = num(cols.dest_lon[i]);
            if ([oLa, oLo, dLa, dLo].some((v) => !Number.isFinite(v))) {
              block[i] = blank("error");
            } else {
              sendIdx.push(i);
              // Datenminimierung: NUR die vier Koordinaten verlassen das Blatt (keine IDs,
              // keine weiteren Spalten), auf 6 Nachkommastellen (~0,1 m) gerundet.
              send.push({ origin_lat: r6(oLa), origin_lon: r6(oLo), dest_lat: r6(dLa), dest_lon: r6(dLo) });
            }
          }
        }
        if (send.length) {
          const res = await postBatch(send);
          res.forEach((r, k) => (block[sendIdx[k]] = r));
        }
        for (const r of block) {
          if (r.status === "ok") counts.ok++;
          else if (r.status === "snapped_far") counts.far++;
          else counts.bad++;
        }

        await writeBlock(c, startCol, start, block, spec); // Teilergebnis sofort sichtbar
        done += len;
        setProgress(done, n);
      }
      summarize(counts, (performance.now() - t0) / 1000, spec.length);
    } catch (e) {
      showAlert(e && e.message ? e.message : String(e));
    } finally {
      setRunning(false);
    }
  }

  // Überschriften der Ergebnis-Spalten einmalig schreiben
  async function writeHeader(c, startCol, spec) {
    if (!c.hasHeader) return;
    await Excel.run(async (ctx) => {
      const sheet = ctx.workbook.worksheets.getActiveWorksheet();
      const h = sheet.getRangeByIndexes(c.rowIndex, startCol, 1, spec.length);
      h.values = [spec.map((s) => s.header)];
      h.format.font.bold = true;
      await ctx.sync();
    });
  }

  // 4 Eingabespalten eines Blocks lesen (schlank, konstanter Speicher) — je nach Modus
  // Koordinaten oder LKZ/PLZ; map enthält genau die aktiven Spalten.
  async function readBlock(c, map, start, len) {
    const out = {};
    const keys = Object.keys(map);
    await Excel.run(async (ctx) => {
      const sheet = ctx.workbook.worksheets.getActiveWorksheet();
      const startRow = c.rowIndex + (c.hasHeader ? 1 : 0) + start;
      const ranges = {};
      keys.forEach((t) => {
        const r = sheet.getRangeByIndexes(startRow, c.columnIndex + map[t], len, 1);
        r.load("values");
        ranges[t] = r;
      });
      await ctx.sync();
      keys.forEach((t) => (out[t] = ranges[t].values.map((row) => row[0])));
    });
    return out;
  }

  // Ergebnis-Block rechts neben den Bereich schreiben
  async function writeBlock(c, startCol, start, block, spec) {
    const dataRow = c.rowIndex + (c.hasHeader ? 1 : 0) + start;
    const body = block.map((r) => spec.map((s) => s.val(r)));
    await Excel.run(async (ctx) => {
      const sheet = ctx.workbook.worksheets.getActiveWorksheet();
      sheet.getRangeByIndexes(dataRow, startCol, body.length, spec.length).values = body;
      await ctx.sync();
    });
  }

  async function postBatch(pairs) {
    const body = JSON.stringify({ pairs });
    const MAX = 3; // bei transienten Netzfehlern bis zu 3 Versuche pro Block
    let lastErr;
    for (let attempt = 1; attempt <= MAX; attempt++) {
      try {
        const resp = await fetch(`${API}/route-batch`, {
          method: "POST",
          headers: { "Content-Type": "application/json", ...authHeaders() },
          body,
        });
        if (resp.status === 401) {
          state.authed = false;
          setGate(true);
          throw new Error("Zugangstoken abgelaufen — bitte neu verbinden.");
        }
        if (resp.status >= 500 || resp.status === 429) {
          lastErr = new Error(`HTTP ${resp.status}`); // transient → wiederholen
          if (attempt < MAX) { await retryWait(attempt); continue; }
          throw lastErr;
        }
        if (!resp.ok) {
          let detail = `HTTP ${resp.status}`;
          try { detail = (await resp.json()).detail || detail; } catch {}
          throw new Error(detail); // 4xx (z. B. 413) → nicht wiederholen
        }
        return (await resp.json()).results;
      } catch (e) {
        // Netzwerkfehler (fetch wirft TypeError) → wiederholen
        if (e instanceof TypeError && attempt < MAX) { lastErr = e; await retryWait(attempt); continue; }
        throw e;
      }
    }
    throw lastErr || new Error("Netzwerkfehler");
  }

  function retryWait(attempt) {
    $("progLabel").textContent = "Verbindung unterbrochen — neuer Versuch …";
    return new Promise((r) => setTimeout(r, Math.min(600 * 2 ** (attempt - 1), 4000)));
  }

  function summarize(counts, secs, ncols) {
    $("rTotal").textContent = counts.total.toLocaleString("de-DE");
    $("rOk").textContent = counts.ok.toLocaleString("de-DE");
    $("rFar").textContent = counts.far.toLocaleString("de-DE");
    $("rBad").textContent = counts.bad.toLocaleString("de-DE");
    $("elapsed").textContent = secs < 1 ? `${Math.round(secs * 1000)} ms` : `${secs.toFixed(1)} s`;
    $("writtenCols").textContent = `${ncols} Spalte${ncols === 1 ? "" : "n"}`;
    $("results").classList.add("is-on");
  }

  // ---------- UI-Helfer ----------
  function setRunning(on) {
    $("runBtn").disabled = on || !state.engineReady;
    $("runLabel").textContent = on ? "Berechne …" : "Strecken berechnen";
    $("runIcon").classList.toggle("spin", on);
    $("progress").classList.toggle("is-on", on);
    if (!on) updateRunState();
  }
  function setProgress(done, total) {
    const pct = total ? Math.round((done / total) * 100) : 0;
    $("progFill").style.width = pct + "%";
    $("progPct").textContent = pct + " %";
    $("progLabel").textContent = `${done.toLocaleString("de-DE")} / ${total.toLocaleString("de-DE")}`;
  }
  function showAlert(msg) { $("alertText").textContent = msg; $("alert").classList.add("is-on"); }
  function hideAlert() { $("alert").classList.remove("is-on"); }

  function colLetter(idx) {
    let s = "";
    idx += 1;
    while (idx > 0) { const m = (idx - 1) % 26; s = String.fromCharCode(65 + m) + s; idx = Math.floor((idx - 1) / 26); }
    return s;
  }
  const num = (v) => (v === "" || v == null ? NaN : Number(v));
  const str = (v) => (v == null ? "" : String(v).trim());
  const numOrBlank = (v) => (v == null ? "" : v);
  // Leeres Ergebnisobjekt (alle Felder, damit outputSpec auch Koordinatenspalten findet)
  const blank = (status) => ({
    distance_km: null, duration_min: null, status, snap_m: null,
    origin_lat: null, origin_lon: null, dest_lat: null, dest_lon: null,
  });
  const r6 = (v) => Math.round(v * 1e6) / 1e6; // auf ~0,1 m runden
})();
