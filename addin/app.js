/* Kilometrix Office.js Add-in — liest Koordinaten, ruft das lokale Backend
   (/route-batch, same-origin HTTPS) und schreibt Ergebnisse ins Blatt zurück. */

(() => {
  "use strict";

  const API = ""; // same-origin: FastAPI liefert dieses Add-in selbst aus
  const RESULT_COLS = ["distance_km", "duration_min", "status", "snap_m"];
  const TARGETS = ["origin_lat", "origin_lon", "dest_lat", "dest_lon"];
  // Blockgröße fürs Streaming: pro Block wird gelesen → berechnet → zurückgeschrieben.
  // Hält den Speicher konstant und macht das Add-in auch für sehr große Blätter tauglich.
  const BLOCK = 2000;

  const $ = (id) => document.getElementById(id);
  const state = { scope: "used", engineReady: false, ctx: null };

  Office.onReady((info) => {
    if (info.host !== Office.HostType.Excel) {
      showAlert("Bitte in Microsoft Excel öffnen.");
      return;
    }
    $("backendUrl").textContent = location.host;
    wireEvents();
    checkHealth();
    refreshContext();

    // bei Auswahländerung im Markierungs-Modus live aktualisieren
    Excel.run(async (ctx) => {
      ctx.workbook.worksheets.getActiveWorksheet().onSelectionChanged.add(async () => {
        if (state.scope === "selection") refreshContext();
      });
      await ctx.sync();
    }).catch(() => {});
  });

  function wireEvents() {
    $("segUsed").onclick = () => setScope("used");
    $("segSel").onclick = () => setScope("selection");
    $("hasHeader").onchange = refreshContext;
    TARGETS.forEach((t) => ($(t).onchange = updateRunState));
    $("runBtn").onclick = run;
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
    TARGETS.forEach((t) => {
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
    };
  }

  function updateRunState() {
    const c = state.ctx;
    const ready = state.engineReady && c && c.dataRows > 0 && TARGETS.every((t) => $(t).value !== "");
    $("runBtn").disabled = !ready;
  }

  // ---------- Berechnung (Streaming in Blöcken) ----------
  async function run() {
    hideAlert();
    $("results").classList.remove("is-on");
    const c = state.ctx;
    if (!c) return;
    const map = {};
    TARGETS.forEach((t) => (map[t] = parseInt($(t).value, 10)));

    setRunning(true);
    const t0 = performance.now();
    const startCol = c.columnIndex + c.columnCount;
    const counts = { total: c.dataRows, ok: 0, far: 0, bad: 0 };
    const n = c.dataRows;

    try {
      await writeHeader(c, startCol); // Überschriften einmalig

      let done = 0;
      setProgress(0, n);
      for (let start = 0; start < n; start += BLOCK) {
        const len = Math.min(BLOCK, n - start);
        const cols = await readBlock(c, map, start, len);

        // lokale Validierung: ungültige Koordinaten markieren, gültige senden
        const block = new Array(len);
        const send = [];
        const sendIdx = [];
        for (let i = 0; i < len; i++) {
          const oLa = num(cols.origin_lat[i]), oLo = num(cols.origin_lon[i]);
          const dLa = num(cols.dest_lat[i]), dLo = num(cols.dest_lon[i]);
          if ([oLa, oLo, dLa, dLo].some((v) => !Number.isFinite(v))) {
            block[i] = { distance_km: null, duration_min: null, status: "error", snap_m: null };
          } else {
            sendIdx.push(i);
            send.push({ origin_lat: oLa, origin_lon: oLo, dest_lat: dLa, dest_lon: dLo });
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

        await writeBlock(c, startCol, start, block); // Teilergebnis sofort sichtbar
        done += len;
        setProgress(done, n);
      }
      summarize(counts, (performance.now() - t0) / 1000);
    } catch (e) {
      showAlert(e && e.message ? e.message : String(e));
    } finally {
      setRunning(false);
    }
  }

  // Überschriften der Ergebnis-Spalten einmalig schreiben
  async function writeHeader(c, startCol) {
    if (!c.hasHeader) return;
    await Excel.run(async (ctx) => {
      const sheet = ctx.workbook.worksheets.getActiveWorksheet();
      const h = sheet.getRangeByIndexes(c.rowIndex, startCol, 1, 4);
      h.values = [RESULT_COLS];
      h.format.font.bold = true;
      await ctx.sync();
    });
  }

  // 4 Koordinatenspalten eines Blocks lesen (schlank, konstanter Speicher)
  async function readBlock(c, map, start, len) {
    const out = {};
    await Excel.run(async (ctx) => {
      const sheet = ctx.workbook.worksheets.getActiveWorksheet();
      const startRow = c.rowIndex + (c.hasHeader ? 1 : 0) + start;
      const ranges = {};
      TARGETS.forEach((t) => {
        const r = sheet.getRangeByIndexes(startRow, c.columnIndex + map[t], len, 1);
        r.load("values");
        ranges[t] = r;
      });
      await ctx.sync();
      TARGETS.forEach((t) => (out[t] = ranges[t].values.map((row) => row[0])));
    });
    return out;
  }

  // Ergebnis-Block rechts neben den Bereich schreiben
  async function writeBlock(c, startCol, start, block) {
    const dataRow = c.rowIndex + (c.hasHeader ? 1 : 0) + start;
    const body = block.map((r) => [
      numOrBlank(r.distance_km), numOrBlank(r.duration_min), r.status, numOrBlank(r.snap_m),
    ]);
    await Excel.run(async (ctx) => {
      const sheet = ctx.workbook.worksheets.getActiveWorksheet();
      sheet.getRangeByIndexes(dataRow, startCol, body.length, 4).values = body;
      await ctx.sync();
    });
  }

  async function postBatch(pairs) {
    const resp = await fetch(`${API}/route-batch`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ pairs }),
    });
    if (!resp.ok) {
      let detail = `HTTP ${resp.status}`;
      try { detail = (await resp.json()).detail || detail; } catch {}
      throw new Error(detail);
    }
    return (await resp.json()).results;
  }

  function summarize(counts, secs) {
    $("rTotal").textContent = counts.total.toLocaleString("de-DE");
    $("rOk").textContent = counts.ok.toLocaleString("de-DE");
    $("rFar").textContent = counts.far.toLocaleString("de-DE");
    $("rBad").textContent = counts.bad.toLocaleString("de-DE");
    $("elapsed").textContent = secs < 1 ? `${Math.round(secs * 1000)} ms` : `${secs.toFixed(1)} s`;
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
  const numOrBlank = (v) => (v == null ? "" : v);
})();
