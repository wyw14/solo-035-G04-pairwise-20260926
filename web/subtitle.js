const stamp = document.getElementById("stamp");

async function getJSON(url) {
  const response = await fetch(url, { headers: { Accept: "application/json" } });
  const payload = await response.json();
  if (!response.ok) throw new Error(payload.error || response.status);
  return payload;
}

async function postJSON(url, body) {
  const response = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify(body),
  });
  const payload = await response.json();
  if (!response.ok) throw new Error(payload.error || response.status);
  return payload;
}

function esc(value) {
  return String(value ?? "").replace(/[&<>"']/g, (ch) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[ch]));
}

function fillEventSelects(operations) {
  const selects = ["play-event", "track-event", "cue-event"].map((id) => document.getElementById(id));
  const current = selects.map((select) => select.value);
  const options = operations.map((op) =>
    `<option value="${op.event_id}">${esc(op.name)} ${op.event_id.slice(0, 8)}</option>`).join("");
  selects.forEach((select, index) => {
    select.innerHTML = options;
    if (current[index] && operations.some((op) => op.event_id === current[index])) {
      select.value = current[index];
    }
  });
  return operations.length ? operations[0].event_id : "";
}

function renderTracks(detail) {
  const eventID = document.getElementById("track-event").value;
  const tracks = (detail.subtitle_tracks || []).filter((track) => track.event_id === eventID);
  const container = document.getElementById("tracks");
  if (!tracks.length) {
    container.innerHTML = `<p class="muted">该事件尚无语种轨道。先在下方提交一条某语种字幕，轨道会自动建立。</p>`;
    return;
  }
  container.innerHTML = tracks.map((track) => {
    const cues = track.cues || [];
    const cueRows = cues.map((cue) => `
      <tr>
        <td>${cue.start_ms}–${cue.end_ms}</td>
        <td>${esc(cue.text)}</td>
        <td>${esc(cue.revision.slice(0, 8))}</td>
        <td>${cue.reviewed
          ? `<span class="tag ok">已审</span>`
          : `<span class="tag warn">待审</span> <button class="secondary" data-review data-cue="${esc(cue.id)}" data-lang="${esc(track.language)}">通过</button>`}</td>
      </tr>`).join("");
    return `<div style="margin-bottom:18px">
      <h3 style="font-size:14px;margin:0 0 8px">${esc(track.language)}
        ${track.re_review_pending ? `<span class="tag bad">需重审</span>` : `<span class="tag ok">有效</span>`}</h3>
      <table><thead><tr><th>时码(ms)</th><th>译文</th><th>版本</th><th>审核</th></tr></thead><tbody>${cueRows}</tbody></table>
    </div>`;
  }).join("");
  container.querySelectorAll("[data-review]").forEach((button) => {
    button.addEventListener("click", async () => {
      try {
        await postJSON("/api/subtitle-review", {
          event_id: eventID,
          language: button.dataset.lang,
          cue_id: button.dataset.cue,
        });
        await refresh();
      } catch (error) { alert(error.message); }
    });
  });
}

async function runAudit() {
  const eventID = document.getElementById("track-event").value;
  const target = document.getElementById("audit");
  if (!eventID) { target.innerHTML = ""; return; }
  try {
    const report = await getJSON(`/api/subtitle-audit?event_id=${eventID}`);
    if (!report.issues.length) {
      target.innerHTML = `<p><span class="tag ok">对齐</span> ${report.languages.length} 个语种在同一画面时段全部有效对齐</p>`;
      return;
    }
    const rows = report.issues.map((issue) => `
      <tr><td>${esc(issue.language)}</td><td><span class="tag bad">${esc(issue.kind)}</span></td>
      <td>${issue.start_ms ?? ""}${issue.end_ms ? "–" + issue.end_ms : ""}</td>
      <td>${esc(issue.detail)}</td></tr>`).join("");
    target.innerHTML = `<table><thead><tr><th>语种</th><th>问题</th><th>时码(ms)</th><th>说明</th></tr></thead><tbody>${rows}</tbody></table>`;
  } catch (error) {
    target.innerHTML = `<p class="tag bad">${esc(error.message)}</p>`;
  }
}

function renderReReview(items) {
  const container = document.getElementById("rereview");
  const rows = items || [];
  if (!rows.length) {
    container.innerHTML = `<p class="tag ok">没有因节目修订而需要重审的轨道</p>`;
    return;
  }
  container.innerHTML = `<table><thead><tr><th>事件</th><th>语种</th><th>过期条数</th><th>旧版本</th><th>当前版本</th></tr></thead><tbody>` +
    rows.map((item) => `<tr><td>${esc(item.event_id.slice(0, 8))}</td><td>${esc(item.language)}</td>
      <td>${item.stale_cues}</td><td>${esc((item.from_revision || "").slice(0, 8))}</td>
      <td>${esc((item.current_revision || "").slice(0, 8))}</td></tr>`).join("") +
    `</tbody></table>`;
}

async function refresh() {
  try {
    const [operations, detail, rereview] = await Promise.all([
      getJSON("/api/operations"),
      getJSON("/api/detail"),
      getJSON("/api/subtitle-rereview"),
    ]);
    fillEventSelects(operations.operations || []);
    renderTracks(detail);
    renderReReview(rereview.rereview);
    stamp.textContent = `状态正常 · ${new Date().toLocaleTimeString()}`;
  } catch (error) {
    stamp.textContent = `服务连接失败：${error.message}`;
  }
}

document.getElementById("play-query").addEventListener("click", async () => {
  const target = document.getElementById("playback-result");
  try {
    const params = new URLSearchParams({
      event_id: document.getElementById("play-event").value,
      language: document.getElementById("play-language").value.trim(),
      timecode_ms: document.getElementById("play-timecode").value,
    });
    const result = await getJSON(`/api/subtitles?${params}`);
    target.className = "";
    target.textContent = result.active
      ? `${result.cue.start_ms}–${result.cue.end_ms}ms · ${result.cue.text}`
      : "此时段没有有效字幕（未审或过期字幕不会播出）";
  } catch (error) {
    target.className = "tag bad";
    target.textContent = error.message;
  }
});

document.getElementById("audit-run").addEventListener("click", runAudit);
document.getElementById("track-event").addEventListener("change", () => refresh().then(runAudit));

document.getElementById("cue-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  try {
    await postJSON("/api/subtitle-cues", {
      event_id: document.getElementById("cue-event").value,
      language: document.getElementById("cue-language").value.trim(),
      start_ms: Number(document.getElementById("cue-start").value),
      end_ms: Number(document.getElementById("cue-end").value),
      text: document.getElementById("cue-text").value,
    });
    document.getElementById("cue-text").value = "";
    await refresh();
    await runAudit();
  } catch (error) { alert(error.message); }
});

refresh().then(runAudit);
setInterval(refresh, 5000);
