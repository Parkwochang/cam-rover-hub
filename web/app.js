"use strict";
const $ = (id) => document.getElementById(id);
const cockpit = $("cockpit"),
  video = $("video"),
  dialog = $("settingsDialog");
let socket,
  socketTimer,
  operatorID = 0,
  reconnectDelay = 500,
  closing = false;
let activeDirection = "stop",
  heldPointer = null,
  controlState = { mode: "manual" },
  videoHealthy = false,
  socketReady = false;
let stoppedLatch = true,
  lightOn = false,
  videoTimer,
  toastTimer,
  videoRetry = 1000;
let activating = false;
let activationCancelled = false;
let requestSequence = 0;
let controlRevision = 0;
let driveRequestID = 0;
let autoEnabled = false;
let pollGeneration = 0;
let mapPoses = [],
  mapTracking = false,
  scanPending = false;
const acknowledgements = new Map();
const defaults = {
  showMap: true,
  mapSize: 240,
  mapOpacity: 80,
  followMap: false,
  videoFit: "cover",
};
let prefs = { ...defaults };
try {
  const saved = JSON.parse(localStorage.getItem("rover-deck-settings") || "{}");
  prefs = {
    showMap: typeof saved.showMap === "boolean" ? saved.showMap : true,
    mapSize: Math.max(170, Math.min(320, Number(saved.mapSize) || 240)),
    mapOpacity: Math.max(40, Math.min(100, Number(saved.mapOpacity) || 80)),
    followMap: saved.followMap === true,
    videoFit: saved.videoFit === "contain" ? "contain" : "cover",
  };
} catch (_) {}
function toast(message) {
  clearTimeout(toastTimer);
  $("toast").textContent = message;
  $("toast").hidden = false;
  toastTimer = setTimeout(() => ($("toast").hidden = true), 4500);
}
const send = (message) => {
  if (socket?.readyState !== WebSocket.OPEN) return;
  // Never queue old movement while a slow command is still awaiting its ACK.
  if (message.type === "drive" && driveRequestID) return;
  const requestID = message.request_id ?? ++requestSequence;
  if (message.type === "drive") driveRequestID = requestID;
  socket.send(JSON.stringify({ ...message, request_id: requestID }));
};
function releaseDrive() {
  activeDirection = "stop";
  heldPointer = null;
  document
    .querySelectorAll(".held")
    .forEach((button) => button.classList.remove("held"));
}
function stop() {
  if (activating) activationCancelled = true;
  releaseDrive();
  send({ type: "stop" });
}
function canDrive() {
  return (
    socketReady &&
    videoHealthy &&
    !stoppedLatch &&
    !activating &&
    controlState.mode === "manual" &&
    !dialog.open &&
    !document.hidden
  );
}
function updateControls() {
  document.querySelectorAll("[data-direction]").forEach((button) => {
    button.disabled = button.dataset.direction !== "stop" && !canDrive();
  });
  $("manualMode").disabled =
    !socketReady || !videoHealthy || dialog.open || activating;
  $("manualMode").textContent = "수동 모드";
  $("manualMode").title = stoppedLatch ? "수동 모드 활성화" : "수동 모드";
  $("manualMode").setAttribute("aria-pressed", String(controlState.mode === "manual"));
  $("autoMode").textContent = "자동 모드";
  $("autoMode").setAttribute("aria-pressed", String(controlState.mode === "auto"));
  $("autoMode").disabled = !autoEnabled || !socketReady || !videoHealthy || dialog.open || activating;
  $("speed").disabled = !socketReady || stoppedLatch;
  $("light").disabled = !socketReady;
  $("linkDot").classList.toggle("online", socketReady && videoHealthy);
  cockpit.classList.toggle("video-offline", !videoHealthy);
  $("videoNotice").hidden = videoHealthy;
  $("modeState").textContent = controlState.mode === "auto" ? "자동 모드" : "수동 모드";
  $("status").textContent = !socketReady
    ? "허브 연결 대기"
    : !videoHealthy
      ? "로봇 영상 연결 대기"
      : stoppedLatch
        ? "연결됨 · 수동 활성화 필요"
        : controlState.mode === "auto" ? "연결됨 · 자동 모드" : "연결됨 · 수동 모드";
}
function ack(message) {
  return new Promise((resolve, reject) => {
    if (!socketReady) {
      reject(new Error("허브 조작 연결을 확인하세요."));
      return;
    }
    if ([...acknowledgements.values()].some((pending) => pending.command === message.type)) {
      reject(new Error("이전 명령을 처리 중입니다."));
      return;
    }
    const requestID = ++requestSequence;
    const timer = setTimeout(() => {
      acknowledgements.delete(requestID);
      reject(new Error("조작 응답 시간 초과"));
    }, message.type === "activate" ? 5500 : 3000);
    acknowledgements.set(requestID, { timer, resolve, reject, command: message.type });
    send({ ...message, request_id: requestID });
  });
}
function rejectPending(message) {
  for (const pending of acknowledgements.values()) {
    clearTimeout(pending.timer);
    pending.reject(new Error(message));
  }
  acknowledgements.clear();
}
async function superviseForAction() {
  releaseDrive();
  await ack({ type: "stop" });
  await ack({ type: "supervise", active: true });
}
function connectSocket() {
  clearTimeout(socketTimer);
  if (closing) return;
  const next = new WebSocket(
    `${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}/ws`,
  );
  socket = next;
  next.onopen = () => {
    if (socket !== next) return;
    reconnectDelay = 500;
    socketReady = true;
    stoppedLatch = true;
    updateControls();
  };
  next.onmessage = (event) => {
    if (socket !== next) return;
    let message;
    try {
      message = JSON.parse(event.data);
    } catch (_) {
      return;
    }
    if (message.type === "hello") {
      operatorID = message.operator_id;
      if (!document.hidden) send({ type: "supervise", active: true });
    }
    if (message.type === "ack") {
      if (message.request_id === driveRequestID) driveRequestID = 0;
      const pending = acknowledgements.get(message.request_id);
      if (pending && pending.command === message.command) {
        acknowledgements.delete(message.request_id);
        clearTimeout(pending.timer);
        pending.resolve(message);
      }
    }
    if (message.type === "error") {
      driveRequestID = 0;
      rejectPending(message.message);
      stoppedLatch = true;
      if (message.command === "stop") {
        activationCancelled = true;
        releaseDrive();
      } else stop();
      toast(message.message);
      updateControls();
    }
  };
  next.onclose = () => {
    if (socket !== next) return;
    socketReady = false;
    driveRequestID = 0;
    operatorID = 0;
    stoppedLatch = true;
    rejectPending("허브 연결이 끊겼습니다. 다시 활성화하세요.");
    stop();
    updateControls();
    if (!closing) socketTimer = setTimeout(connectSocket, reconnectDelay);
    reconnectDelay = Math.min(reconnectDelay * 2, 5000);
  };
  next.onerror = () => {
    if (socket === next) next.close();
  };
}
document.querySelectorAll("[data-direction]").forEach((button) => {
  button.addEventListener("pointerdown", (event) => {
    event.preventDefault();
    if (button.dataset.direction === "stop") {
      stop();
      toast("정지 명령 전송");
      return;
    }
    if (!canDrive() || heldPointer !== null) return;
    heldPointer = event.pointerId;
    button.setPointerCapture(event.pointerId);
    button.classList.add("held");
    activeDirection = button.dataset.direction;
    send({ type: "drive", direction: activeDirection });
  });
  for (const name of ["pointerup", "pointercancel", "lostpointercapture"])
    button.addEventListener(name, (event) => {
      if (heldPointer === event.pointerId) stop();
    });
  button.addEventListener("contextmenu", (event) => event.preventDefault());
});
$("emergencyStop").addEventListener("click", stop);
setInterval(() => {
  if (activeDirection !== "stop") {
    if (!canDrive()) stop();
    else send({ type: "drive", direction: activeDirection });
  }
  if (!document.hidden && document.hasFocus() && operatorID)
    send({ type: "supervise", active: true });
}, 250);
addEventListener("blur", () => {
  stop();
  send({ type: "supervise", active: false });
});
addEventListener("pagehide", () => {
  closing = true;
  pollGeneration++;
  rejectPending("페이지를 종료합니다.");
  stop();
  send({ type: "supervise", active: false });
  clearTimeout(socketTimer);
  socket?.close();
});
addEventListener("pageshow", (event) => {
  if (event.persisted) {
    closing = false;
    connectSocket();
    loadVideo();
    refreshStatus();
    startPolling();
  }
});
document.addEventListener("visibilitychange", () => {
  stop();
  if (document.hidden) {
    send({ type: "supervise", active: false });
    stoppedLatch = true;
    clearTimeout(videoTimer);
    video.removeAttribute("src");
  } else {
    loadVideo();
    refreshStatus();
  }
  updateControls();
});
async function jsonRequest(path, options = {}) {
  const controller = new AbortController(),
    timer = setTimeout(
      () => controller.abort(),
      path === "/api/maps/save" ? 20000 : 8000,
    );
  try {
    const response = await fetch(path, {
      ...options,
      cache: "no-store",
      signal: controller.signal,
      headers: { "Content-Type": "application/json", ...options.headers },
    });
    const data = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`);
    return data;
  } catch (error) {
    if (error.name === "AbortError")
      throw new Error("응답 시간 초과 · 로봇 전원과 주소를 확인하세요.");
    throw error;
  } finally {
    clearTimeout(timer);
  }
}
function loadVideo() {
  clearTimeout(videoTimer);
  if (!document.hidden) video.src = `/video.mjpeg?t=${Date.now()}`;
}
video.addEventListener("error", () => {
  clearTimeout(videoTimer);
  if (!document.hidden) {
    videoTimer = setTimeout(loadVideo, videoRetry);
    videoRetry = Math.min(5000, videoRetry * 2);
  }
});
video.addEventListener("load", () => {
  videoRetry = 1000;
});
async function refreshStatus() {
  const revision = controlRevision;
  try {
    const [state, link] = await Promise.all([
      jsonRequest("/api/status"),
      jsonRequest("/api/link"),
    ]);
    if (revision === controlRevision) controlState = state;
    if (revision === controlRevision && state.fault) {
      stoppedLatch = true;
      if (activeDirection !== "stop") stop();
    }
    const wasHealthy = videoHealthy;
    videoHealthy = link.video?.healthy === true;
    if (wasHealthy && !videoHealthy) {
      stoppedLatch = true;
      stop();
    }
    if (!wasHealthy && videoHealthy) loadVideo();
    if (
      document.activeElement !== $("roverAddress") &&
      !$("roverAddress").dataset.dirty
    )
      $("roverAddress").value = link.address || "";
    $("videoMessage").textContent = state.fault
      ? `안전 정지: ${state.fault}`
      : "로봇 전원·집 Wi-Fi·주소를 확인하세요. 연결 복구 후 수동 제어를 활성화하세요.";
    autoEnabled = state.auto_enabled === true;
  } catch (error) {
    videoHealthy = false;
    stoppedLatch = true;
    stop();
    $("videoMessage").textContent = error.message;
  }
  updateControls();
}
$("manualMode").addEventListener("click", async () => {
  if (activating) return;
  activating = true;
  activationCancelled = false;
  controlRevision++;
  stoppedLatch = true;
  updateControls();
  try {
    releaseDrive();
    const result = await ack({ type: "activate", speed: Number($("speed").value) });
    if (activationCancelled || document.hidden || dialog.open || !socketReady)
      throw new Error("활성화를 취소했습니다. 다시 수동 제어를 활성화하세요.");
    controlState = result.status;
    stoppedLatch = false;
    toast("수동 제어 활성화 · 버튼을 누르는 동안만 주행");
    updateControls();
  } catch (error) {
    stoppedLatch = true;
    stop();
    toast(error.message);
  } finally {
    controlRevision++;
    activating = false;
    updateControls();
  }
});
$("autoMode").addEventListener("click", async () => {
  if ($("autoMode").disabled) return;
  if (!confirm("바퀴·배터리·정지 안전 검증과 현장 감독을 완료했나요?")) return;
  try {
    await superviseForAction();
    const result = await jsonRequest("/api/mode", {
      method: "POST",
      body: JSON.stringify({ mode: "auto", operator_id: operatorID }),
    });
    toast(result.message || "모드 전환 요청 완료");
    refreshStatus();
  } catch (error) {
    toast(error.message);
  }
});
$("speed").addEventListener("input", () => {
  $("speedValue").value = $("speed").value;
  $("speedReadout").textContent = `SPD ${$("speed").value}`;
});
$("speed").addEventListener("change", () => {
  stop();
  send({ type: "speed", speed: Number($("speed").value) });
});
$("light").addEventListener("click", () => {
  lightOn = !lightOn;
  send({ type: "light", on: lightOn });
  $("light").textContent = lightOn ? "라이트 끄기" : "라이트 켜기";
});
function selectTab(tab) {
  document
    .querySelectorAll("[data-tab]")
    .forEach((button) =>
      button.setAttribute("aria-selected", String(button.dataset.tab === tab)),
    );
  document
    .querySelectorAll("[data-panel]")
    .forEach((panel) => (panel.hidden = panel.dataset.panel !== tab));
}
function openSettings(tab = "connection") {
  stop();
  selectTab(tab);
  if (!dialog.open) dialog.showModal();
  updateControls();
}
$("openSettings").addEventListener("click", () => openSettings());
$("mapSettings").addEventListener("click", () => openSettings("map"));
$("closeSettings").addEventListener("click", () => dialog.close());
dialog.addEventListener("close", () => {
  $("password").value = "";
  stop();
  updateControls();
});
document
  .querySelectorAll("[data-tab]")
  .forEach((button) =>
    button.addEventListener("click", () => selectTab(button.dataset.tab)),
  );
$("fullscreen").addEventListener("click", async () => {
  stop();
  try {
    if (document.fullscreenElement) await document.exitFullscreen();
    else {
      await cockpit.requestFullscreen();
      if (screen.orientation?.lock)
        await screen.orientation.lock("landscape").catch(() => {});
    }
  } catch (_) {
    toast("이 브라우저는 전체 화면을 지원하지 않습니다. 가로로 돌려주세요.");
  }
});
$("roverAddress").addEventListener(
  "input",
  () => ($("roverAddress").dataset.dirty = "true"),
);
$("addressForm").addEventListener("submit", async (event) => {
  event.preventDefault();
  stop();
  stoppedLatch = true;
  $("saveAddress").disabled = true;
  try {
    const result = await jsonRequest("/api/link", {
      method: "PUT",
      body: JSON.stringify({ address: $("roverAddress").value }),
    });
    $("roverAddress").value = result.address;
    delete $("roverAddress").dataset.dirty;
    $("addressMessage").textContent =
      "주소 저장 완료 · 재연결 중입니다. 복구 후 수동 제어를 활성화하세요.";
    loadVideo();
    refreshStatus();
  } catch (error) {
    $("addressMessage").textContent = error.message;
  } finally {
    $("saveAddress").disabled = false;
    updateControls();
  }
});
async function reconnect() {
  stop();
  stoppedLatch = true;
  try {
    await jsonRequest("/api/link/reconnect", { method: "POST" });
    loadVideo();
    toast("정지 상태로 재연결 중");
  } catch (error) {
    toast(error.message);
  }
  updateControls();
}
$("reconnect").addEventListener("click", reconnect);
$("retryVideo").addEventListener("click", reconnect);
async function refreshNetwork() {
  try {
    const data = await jsonRequest("/api/network");
    $("networkStatus").textContent =
      `${data.mode === "sta" ? "집 Wi-Fi" : "로봇 AP"} · ${data.phase || "대기"} · ${data.ip || ""} · ${data.saved_ssid || "저장된 Wi-Fi 없음"}`;
    if (data.last_error) $("networkMessage").textContent = data.last_error;
  } catch (_) {
    $("networkStatus").textContent =
      "로봇 연결 대기 · 전원/IP/호스트명을 확인하세요.";
  }
}
$("connectAP").addEventListener("click", async () => {
  if (
    !confirm(
      "파이의 Wi-Fi를 로봇 AP로 전환합니다. 유선/다른 인터넷 연결이 없으면 허브 연결이 끊깁니다. 계속할까요?",
    )
  )
    return;
  stop();
  try {
    await jsonRequest("/api/link/ap", { method: "POST" });
    refreshNetwork();
    loadVideo();
  } catch (error) {
    $("networkMessage").textContent = error.message;
  }
});
$("scanWiFi").addEventListener("click", async () => {
  stop();
  try {
    await jsonRequest("/api/wifi/scan", { method: "POST" });
    scanPending = true;
    $("networkMessage").textContent =
      "Wi-Fi 검색 중 · 영상이 잠시 끊길 수 있습니다.";
  } catch (error) {
    $("networkMessage").textContent = error.message;
  }
});
async function refreshScan() {
  if (!scanPending) return;
  try {
    const result = await jsonRequest("/api/wifi/scan");
    if (result.phase === "scanning") return;
    scanPending = false;
    $("networkList").replaceChildren(new Option("검색 결과 선택", ""));
    for (const item of result.networks || [])
      $("networkList").add(
        new Option(`${item.ssid} · ${item.rssi} dBm`, item.ssid),
      );
    $("networkMessage").textContent = result.error || "검색 완료";
  } catch (_) {
    scanPending = false;
  }
}
$("networkList").addEventListener("change", (event) => {
  if (event.target.value) $("ssid").value = event.target.value;
});
async function changeNetwork(mode) {
  if (
    mode === "ap" &&
    !confirm(
      "로봇을 AP 모드로 전환합니다. 파이의 별도 유선 연결이 없으면 허브가 끊길 수 있습니다. 계속할까요?",
    )
  )
    return;
  const ssid = $("ssid").value,
    password = $("password").value;
  if (mode === "sta" && !!ssid !== !!password) {
    $("networkMessage").textContent =
      "SSID와 Wi-Fi 암호를 함께 입력하거나, 저장된 설정을 쓰려면 둘 다 비우세요.";
    return;
  }
  stop();
  try {
    await jsonRequest("/api/network", {
      method: "POST",
      body: JSON.stringify({
        mode,
        ...(mode === "sta" && ssid && password ? { ssid, password } : {}),
      }),
    });
    $("networkMessage").textContent =
      "전환 시작 · 실제 연결 성공 후 Wi-Fi 설정이 저장됩니다.";
  } catch (error) {
    $("networkMessage").textContent = error.message;
  } finally {
    $("password").value = "";
  }
}
$("useHome").addEventListener("click", () => changeNetwork("sta"));
$("useAP").addEventListener("click", () => changeNetwork("ap"));
function applyPrefs() {
  $("minimap").hidden = !prefs.showMap;
  cockpit.style.setProperty("--map-size", `${prefs.mapSize}px`);
  cockpit.style.setProperty("--map-opacity", prefs.mapOpacity / 100);
  video.style.objectFit = prefs.videoFit;
  for (const id of ["showMap", "followMap"]) $(id).checked = prefs[id];
  for (const id of ["mapSize", "mapOpacity", "videoFit"])
    $(id).value = prefs[id];
  $("mapSizeValue").value = prefs.mapSize;
  $("mapOpacityValue").value = prefs.mapOpacity;
  drawMap();
}
for (const id of Object.keys(defaults))
  $(id).addEventListener("input", () => {
    prefs[id] =
      $(id).type === "checkbox"
        ? $(id).checked
        : $(id).type === "range"
          ? Number($(id).value)
          : $(id).value;
    try {
      localStorage.setItem("rover-deck-settings", JSON.stringify(prefs));
    } catch (_) {}
    applyPrefs();
  });
function drawMap() {
  const canvas = $("map"),
    width = canvas.clientWidth,
    height = canvas.clientHeight;
  if (!width || !height) return;
  const ratio = Math.min(devicePixelRatio || 1, 2);
  canvas.width = Math.round(width * ratio);
  canvas.height = Math.round(height * ratio);
  const ctx = canvas.getContext("2d");
  ctx.scale(ratio, ratio);
  ctx.clearRect(0, 0, width, height);
  ctx.strokeStyle = "#bfd6f312";
  ctx.lineWidth = 1;
  for (let x = 0; x < width; x += 24) {
    ctx.beginPath();
    ctx.moveTo(x, 0);
    ctx.lineTo(x, height);
    ctx.stroke();
  }
  for (let y = 0; y < height; y += 24) {
    ctx.beginPath();
    ctx.moveTo(0, y);
    ctx.lineTo(width, y);
    ctx.stroke();
  }
  if (!mapPoses.length) {
    ctx.fillStyle = "#7c8da4";
    ctx.font = "10px system-ui";
    ctx.textAlign = "center";
    ctx.fillText("추정 위치 없음", width / 2, height / 2);
    return;
  }
  let minX = Infinity,
    maxX = -Infinity,
    minY = Infinity,
    maxY = -Infinity;
  for (const p of mapPoses) {
    minX = Math.min(minX, p.x);
    maxX = Math.max(maxX, p.x);
    minY = Math.min(minY, p.y);
    maxY = Math.max(maxY, p.y);
  }
  const latest = mapPoses[mapPoses.length - 1],
    centerX = prefs.followMap ? latest.x : (minX + maxX) / 2,
    centerY = prefs.followMap ? latest.y : (minY + maxY) / 2;
  const spanX = 2 * Math.max(maxX - centerX, centerX - minX, 0.005),
    spanY = 2 * Math.max(maxY - centerY, centerY - minY, 0.005),
    scale = Math.min((width - 28) / spanX, (height - 28) / spanY);
  const point = (p) => ({
    x: width / 2 + (p.x - centerX) * scale,
    y: height / 2 - (p.y - centerY) * scale,
  });
  ctx.beginPath();
  mapPoses.forEach((p, i) => {
    const q = point(p);
    if (i) ctx.lineTo(q.x, q.y);
    else ctx.moveTo(q.x, q.y);
  });
  ctx.strokeStyle = "#a2e8c7";
  ctx.lineWidth = 1.8;
  ctx.stroke();
  const tip = point(latest);
  ctx.save();
  ctx.translate(tip.x, tip.y);
  ctx.rotate(-latest.heading);
  ctx.fillStyle = mapTracking ? "#a2e8c7" : "#e7ae61";
  ctx.beginPath();
  ctx.moveTo(0, -7);
  ctx.lineTo(-5, 5);
  ctx.lineTo(5, 5);
  ctx.closePath();
  ctx.fill();
  ctx.restore();
}
async function refreshMaps() {
  try {
    const [list, state] = await Promise.all([
      jsonRequest("/api/maps"),
      jsonRequest("/api/maps/status"),
    ]);
    const selected = $("mapList").value;
    $("mapList").replaceChildren(new Option("지도 선택", ""));
    for (const item of list.maps || [])
      $("mapList").add(
        new Option(`${item.name} · ${item.status}`, String(item.id)),
      );
    $("mapList").value = selected || (state.map_id ? String(state.map_id) : "");
    const id = state.map_id || Number($("mapList").value);
    $("mapStatus").textContent = state.running
      ? state.tracking
        ? "상대 위치 추적 중"
        : "추적 대기 · 정지 필요"
      : "위치 추적 대기";
    $("mapDetail").textContent =
      state.error ||
      (state.running
        ? `지도 #${id} · ${state.tracking ? "추적 중" : "위치 추적 대기/실패"}`
        : "지도 작업 없음 · 비전 워커 설정 필요");
    mapTracking = state.running && state.tracking;
    if (id) {
      const result = await jsonRequest(`/api/maps/${id}/poses`);
      mapPoses = (result.poses || [])
        .filter(
          (p) =>
            Number.isFinite(p.x) &&
            Number.isFinite(p.y) &&
            Number.isFinite(p.heading),
        )
        .slice(-2000);
    } else mapPoses = [];
    drawMap();
  } catch (error) {
    mapTracking = false;
    $("mapStatus").textContent = "지도 연결 대기";
    $("mapDetail").textContent = error.message;
    drawMap();
  }
}
async function mapAction(path, body) {
  try {
    await superviseForAction();
    await jsonRequest(path, {
      method: "POST",
      body: JSON.stringify({ ...body, operator_id: operatorID }),
    });
    refreshMaps();
  } catch (error) {
    $("mapDetail").textContent = error.message;
  }
}
$("createMap").addEventListener("click", () =>
  mapAction("/api/maps", { name: $("mapName").value }),
);
$("saveMap").addEventListener("click", () => mapAction("/api/maps/save", {}));
$("loadMap").addEventListener("click", () => {
  const id = $("mapList").value;
  if (id) mapAction(`/api/maps/${id}/load`, {});
});
new ResizeObserver(drawMap).observe($("map"));
applyPrefs();
updateControls();
connectSocket();
loadVideo();
refreshStatus();
refreshNetwork();
refreshMaps();
// Schedule after completion: slow/offline devices cannot accumulate requests.
async function poll(task, delay, generation) {
  while (!closing && generation === pollGeneration) {
    await new Promise((resolve) => setTimeout(resolve, delay));
    if (!document.hidden && !closing && generation === pollGeneration)
      await task();
  }
}
function startPolling() {
  const generation = ++pollGeneration;
  poll(refreshStatus, 1000, generation);
  poll(refreshNetwork, 3000, generation);
  poll(refreshScan, 1500, generation);
  poll(refreshMaps, 2500, generation);
}
startPolling();
