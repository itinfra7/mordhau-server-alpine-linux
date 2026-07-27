"use strict";

const app = {
  csrf: "",
  username: "",
  snapshot: null,
  config: null,
  modPlan: null,
  modPlanReference: "",
  modsLoading: false,
  modsReloadRequested: false,
  modRevision: null,
  customPaks: null,
  customPaksLoading: false,
  customPaksReloadRequested: false,
  customPakUploadXHR: null,
  lifecycleObserved: false,
  lifecycleRunning: false,
  lifecycleFinishedAt: "",
  eventSequence: 0,
  eventLines: 0,
  runtimeStatus: null,
  runtimeTarget: null,
  runtimeSelectedID: "",
  runtimeExpandedPlayerKey: "",
  runtimeEditing: null,
  runtimeSaving: false,
  runtimeLoading: false,
  runtimeReloadRequested: false,
  runtimeManualRefreshing: false,
  players: null,
  playerDetail: null,
  playerSelectedID: "",
  playerRevision: null,
  playersLoading: false,
  playersReloadRequested: false,
  playerDetailLoading: false,
  playerDetailRequestSequence: 0,
  playerMutationRunning: false,
};

const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => [...document.querySelectorAll(selector)];
const themeStorageKey = "mordhau-control-theme";
const minimumModRefreshMinutes = 1;
const maximumModRefreshMinutes = 10080;

function setTheme(theme, persist = true) {
  const dark = theme === "dark";
  document.documentElement.dataset.theme = dark ? "dark" : "light";
  const toggle = $("#theme-toggle");
  if (toggle) {
    toggle.setAttribute("aria-pressed", String(dark));
    toggle.setAttribute("aria-label", dark ? "Switch to light mode" : "Switch to dark mode");
    $("#theme-toggle-icon").textContent = dark ? "☀" : "☾";
    $("#theme-toggle-label").textContent = dark ? "Light mode" : "Dark mode";
  }
  if (persist) {
    try {
      localStorage.setItem(themeStorageKey, dark ? "dark" : "light");
    } catch (_) {}
  }
}

function initializeTheme() {
  setTheme(document.documentElement.dataset.theme === "dark" ? "dark" : "light", false);
}

function clearLegacyModRefreshPreference() {
  try {
    localStorage.removeItem("mordhau-mod-refresh-minutes");
  } catch (_) {}
}

function validModRefreshMinutes(value) {
  const minutes = Number(value);
  return Number.isInteger(minutes) &&
    minutes >= minimumModRefreshMinutes &&
    minutes <= maximumModRefreshMinutes
    ? minutes
    : null;
}

async function api(path, options = {}) {
  const settings = { ...options, headers: { ...(options.headers || {}) } };
  if (settings.body && typeof settings.body !== "string") {
    settings.headers["Content-Type"] = "application/json";
    settings.body = JSON.stringify(settings.body);
  }
  if ((settings.method || "GET") !== "GET") {
    settings.headers["X-CSRF-Token"] = app.csrf;
  }
  const response = await fetch(path, settings);
  if (response.status === 401) {
    location.href = "/login";
    throw new Error("Session expired");
  }
  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`;
    let body = null;
    try {
      body = await response.json();
      if (body.error) message = body.error;
    } catch (_) {}
    const error = new Error(message);
    error.status = response.status;
    error.body = body;
    throw error;
  }
  if (response.status === 204 || response.headers.get("content-length") === "0") return null;
  const type = response.headers.get("content-type") || "";
  return type.includes("application/json") ? response.json() : response.text();
}

function toast(message, error = false) {
  const item = document.createElement("div");
  item.className = `toast${error ? " error" : ""}`;
  item.textContent = message;
  $("#toast-stack").append(item);
  setTimeout(() => item.remove(), 4200);
}

function bytes(value) {
  if (!Number.isFinite(value) || value < 0) return "—";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let index = 0;
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024;
    index += 1;
  }
  return `${value.toFixed(index >= 3 ? 2 : 1)} ${units[index]}`;
}

function setMeter(name, percent) {
  const safe = Math.max(0, Math.min(100, Number(percent) || 0));
  $(`#${name}-value`).textContent = `${safe.toFixed(1)}%`;
  $(`#${name}-meter`).style.width = `${safe}%`;
}

function renderSnapshot(snapshot) {
  app.snapshot = snapshot;
  if (Number.isSafeInteger(snapshot.mod_revision)) {
    const changed = app.modRevision !== null &&
      app.modRevision !== snapshot.mod_revision;
    app.modRevision = snapshot.mod_revision;
    if (changed) loadMods();
  }
  if (Number.isSafeInteger(snapshot.player_revision)) {
    const changed = app.playerRevision !== null &&
      app.playerRevision !== snapshot.player_revision;
    app.playerRevision = snapshot.player_revision;
    if (changed && app.players && playersPanelActive()) {
      loadPlayers({ silent: true });
    }
  }
  setMeter("cpu", snapshot.metrics.cpu_percent);
  setMeter("memory", snapshot.metrics.memory.percent);
  setMeter("swap", snapshot.metrics.swap.percent);
  setMeter("disk", snapshot.metrics.disk.percent);
  $("#memory-detail").textContent = `${bytes(snapshot.metrics.memory.used)} / ${bytes(snapshot.metrics.memory.total)}`;
  $("#swap-detail").textContent = snapshot.metrics.swap.total
    ? `${bytes(snapshot.metrics.swap.used)} / ${bytes(snapshot.metrics.swap.total)}`
    : "No swap available";
  $("#disk-detail").textContent = `${bytes(snapshot.metrics.disk.used)} / ${bytes(snapshot.metrics.disk.total)}`;

  const runtime = snapshot.runtime_bridge || {};
  const runtimeReady = runtime.ready === true;
  const playerCount = Number(runtime.player_controller_count);
  $("#players-value").textContent = runtimeReady && Number.isInteger(playerCount)
    ? String(playerCount)
    : "—";
  $(".player-metric-card").classList.toggle("bridge-offline", !runtimeReady);
  $("#runtime-bridge-summary").textContent = runtimeReady
    ? (runtime.game_mode_class || "Runtime bridge ready")
    : `Runtime bridge: ${(runtime.status || "unavailable").replaceAll("_", " ")}`;

  $("#server-dot").classList.toggle("online", snapshot.server_running);
  $("#server-label").textContent = snapshot.server_running ? "Server online" : "Server stopped";
  $("#server-pid").textContent = snapshot.server_running ? `PID ${snapshot.server_pid}` : "PID —";
  $("#pending-banner").classList.toggle("hidden", !snapshot.pending_config);

  const operation = snapshot.operation;
  const finishedAt = operation.finished_at || "";
  const customPaksMayHaveChanged = app.lifecycleObserved &&
    ((app.lifecycleRunning && !operation.running) ||
     (finishedAt && finishedAt !== app.lifecycleFinishedAt));
  app.lifecycleObserved = true;
  app.lifecycleRunning = operation.running;
  app.lifecycleFinishedAt = finishedAt;
  $("#operation-spinner").classList.toggle("hidden", !operation.running);
  if (operation.action) {
    const state = operation.running ? "running" : operation.successful ? "completed" : "failed";
    $("#operation-title").textContent = `${operation.action} · ${state}`;
    $("#operation-output").textContent = operation.output || (operation.running ? "Operation in progress…" : "No output.");
  }
  $$("[data-server-action]").forEach((button) => {
    const action = button.dataset.serverAction;
    button.disabled = operation.running ||
      (action === "start" && snapshot.server_running) ||
      (action === "stop" && !snapshot.server_running) ||
      (action === "update" && snapshot.server_running);
  });

  const languageSelect = $("#language-select");
  if (!languageSelect.options.length) {
    snapshot.languages.forEach((language) => {
      const option = document.createElement("option");
      option.value = language.code;
      option.textContent = `${language.name} (${language.code})`;
      languageSelect.append(option);
    });
  }
  if (languageSelect.value !== snapshot.language) languageSelect.value = snapshot.language;

  $("#event-source-status").textContent = snapshot.event_source_status;
  $("#event-source-status").classList.toggle("connected", snapshot.event_source_connected);
  $("#server-prompt-submit").disabled = operation.running || !snapshot.server_running;
  $("#server-prompt-input").disabled = operation.running || !snapshot.server_running;
  $("#server-prompt-mode").disabled = operation.running || !snapshot.server_running;
  appendServerEvents(snapshot.server_events || []);
  if (customPaksMayHaveChanged) loadCustomPaks({ silent: true });
}

function appendServerEvents(events) {
  const consoleElement = $("#server-event-console");
  let added = false;
  for (const event of events) {
    if (event.sequence <= app.eventSequence) continue;
    app.eventSequence = event.sequence;
    if (event.kind === "system" &&
        (event.text === "RCON connected; all broadcasts enabled" ||
         event.text === "RCON reconnected with the running server's previous settings; all broadcasts enabled" ||
         event.text.startsWith("RCON connection closed:"))) {
      continue;
    }
    const line = document.createElement("div");
    line.className = `console-line ${event.kind}`;
    const timestamp = document.createElement("span");
    timestamp.className = "console-time";
    timestamp.textContent = new Date(event.time).toLocaleTimeString();
    const text = document.createElement("span");
    text.className = "console-text";
    text.textContent = event.text;
    line.append(timestamp, text);
    consoleElement.append(line);
    app.eventLines += 1;
    added = true;
  }
  while (app.eventLines > 400 && consoleElement.firstChild) {
    consoleElement.firstChild.remove();
    app.eventLines -= 1;
  }
  if (added) consoleElement.scrollTop = consoleElement.scrollHeight;
}

async function serverAction(action) {
  try {
    await api("/api/server/action", { method: "POST", body: { action } });
    toast(`${action} operation accepted.`);
  } catch (error) {
    toast(error.message, true);
  }
}

async function submitServerPrompt(event) {
  event.preventDefault();
  const mode = $("#server-prompt-mode").value;
  const input = $("#server-prompt-input");
  const submit = $("#server-prompt-submit");
  submit.disabled = true;
  try {
    if (mode === "say") {
      await api("/api/rcon/message", {
        method: "POST",
        body: { message: input.value },
      });
      toast("Server message sent.");
    } else {
      const result = await api("/api/rcon/command", {
        method: "POST",
        body: { command: input.value },
      });
      appendServerEvents(result.events || []);
      const lines = Number(result.response_lines) || 0;
      const suffix = result.response_truncated ? " · output truncated" : "";
      toast(`Command executed · ${lines} response line${lines === 1 ? "" : "s"}${suffix}.`);
    }
    input.value = "";
    input.focus();
  } catch (error) {
    toast(error.message, true);
  } finally {
    const snapshot = app.snapshot;
    submit.disabled = !snapshot || snapshot.operation.running || !snapshot.server_running;
  }
}

function updateServerPromptMode() {
  const sayMode = $("#server-prompt-mode").value === "say";
  const input = $("#server-prompt-input");
  input.placeholder = sayMode ? "Enter server message" : "Enter RCON command";
  input.autocapitalize = sayMode ? "sentences" : "none";
  input.spellcheck = sayMode;
  $("#server-prompt-help").textContent = sayMode
    ? "SAY sends a multilingual message through the local server-only Unicode bridge."
    : "RCON commands run with full administrator authority; commands and responses are retained.";
  input.focus();
}

const runtimeKindLabels = {
  game_mode: "Game Mode",
  game_state: "Game State",
  player_controller: "Player Controller",
  player_state: "Player State",
  pawn: "Possessed Pawn",
};

function runtimeTargetLabel(target) {
  return runtimeKindLabels[target.kind] || target.kind;
}

function runtimePanelActive() {
  return $("#panel-runtime").classList.contains("active");
}

function runtimePlayerGroups(targets) {
  const players = new Map();
  for (const target of targets || []) {
    if (!Number.isInteger(target.player_slot) || target.player_slot < 0) continue;
    if (!players.has(target.player_slot)) {
      players.set(target.player_slot, {
        slot: target.player_slot,
        name: "",
        playFabID: "",
        targets: new Map(),
      });
    }
    const player = players.get(target.player_slot);
    player.targets.set(target.kind, target);
    if (target.player_name) player.name = target.player_name;
    if (target.playfab_id) player.playFabID = target.playfab_id;
  }
  return [...players.values()].sort((left, right) => left.slot - right.slot);
}

function runtimePlayerKey(player) {
  const controller = player.targets.get("player_controller");
  return player.playFabID || controller?.id || `slot:${player.slot}`;
}

function runtimeTargetButton(target, label = runtimeTargetLabel(target)) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "runtime-target-button";
  button.classList.toggle("active", target.id === app.runtimeSelectedID);
  const name = document.createElement("strong");
  name.textContent = label;
  const className = document.createElement("small");
  className.textContent = target.class;
  button.append(name, className);
  button.addEventListener("click", async () => {
    app.runtimeSelectedID = target.id;
    app.runtimeTarget = null;
    if (target.player_slot >= 0) {
      const player = runtimePlayerGroups(app.runtimeStatus?.targets)
        .find((candidate) => candidate.slot === target.player_slot);
      if (player) app.runtimeExpandedPlayerKey = runtimePlayerKey(player);
    }
    renderRuntimeTargets();
    await loadRuntimeTarget();
  });
  return button;
}

function renderRuntimeTargets() {
  const status = app.runtimeStatus;
  const targetList = $("#runtime-targets");
  targetList.replaceChildren();
  const ready = status && status.ready === true;
  $("#runtime-status").textContent = ready
    ? `${status.player_controller_count} player${status.player_controller_count === 1 ? "" : "s"}`
    : "Bridge unavailable";
  $("#runtime-status").classList.toggle("connected", ready);
  if (!ready || !Array.isArray(status.targets) || !status.targets.length) {
    const empty = document.createElement("p");
    empty.className = "hint";
    empty.textContent = ready ? "No runtime targets are available yet." : "Start the game server and wait for the runtime bridge.";
    targetList.append(empty);
    return;
  }

  for (const kind of ["game_mode", "game_state"]) {
    const target = status.targets.find((candidate) => candidate.kind === kind);
    if (!target) continue;
    const group = document.createElement("section");
    group.className = "runtime-target-group";
    group.append(runtimeTargetButton(target));
    targetList.append(group);
  }

  const players = runtimePlayerGroups(status.targets);
  if (!players.length) return;
  const heading = document.createElement("h3");
  heading.className = "runtime-target-group-title runtime-player-list-title";
  heading.textContent = "Connected players";
  targetList.append(heading);

  const liveKeys = new Set(players.map(runtimePlayerKey));
  if (!liveKeys.has(app.runtimeExpandedPlayerKey)) {
    app.runtimeExpandedPlayerKey = "";
  }
  for (const player of players) {
    const key = runtimePlayerKey(player);
    const expanded = app.runtimeExpandedPlayerKey === key;
    const section = document.createElement("section");
    section.className = `runtime-player${expanded ? " expanded" : ""}`;

    const toggle = document.createElement("button");
    toggle.type = "button";
    toggle.className = "runtime-player-toggle";
    toggle.setAttribute("aria-expanded", String(expanded));
    const identity = document.createElement("span");
    identity.className = "runtime-player-identity";
    const playerName = document.createElement("strong");
    playerName.textContent = player.name || `Player ${player.slot + 1}`;
    const playFabID = document.createElement("small");
    playFabID.textContent = player.playFabID
      ? `PlayFabID · ${player.playFabID}`
      : "PlayFabID unavailable";
    identity.append(playerName, playFabID);
    const marker = document.createElement("span");
    marker.className = "runtime-player-marker";
    marker.textContent = "⌄";
    marker.setAttribute("aria-hidden", "true");
    toggle.append(identity, marker);
    toggle.addEventListener("click", () => {
      app.runtimeExpandedPlayerKey = expanded ? "" : key;
      renderRuntimeTargets();
    });
    section.append(toggle);

    const children = document.createElement("div");
    children.className = "runtime-player-targets";
    children.hidden = !expanded;
    for (const kind of ["player_controller", "player_state", "pawn"]) {
      const target = player.targets.get(kind);
      if (target) children.append(runtimeTargetButton(target));
    }
    section.append(children);
    targetList.append(section);
  }
}

function clearRuntimeInspector(message = "Choose an object from Runtime targets.") {
  app.runtimeTarget = null;
  $("#runtime-target-title").textContent = "Select a runtime target";
  $("#runtime-property-count").textContent = "No target";
  $("#runtime-refresh-properties").disabled = true;
  $("#runtime-class-filter").innerHTML = '<option value="">All inherited classes</option>';
  $("#runtime-properties").replaceChildren();
  $("#runtime-empty").textContent = message;
  $("#runtime-empty").classList.remove("hidden");
}

async function loadRuntimeTargets({ selectDefault = false, silent = false } = {}) {
  try {
    const status = await api("/api/runtime/status");
    app.runtimeStatus = status;
    const targets = Array.isArray(status.targets) ? status.targets : [];
    const selectedStillExists = targets.some((target) => target.id === app.runtimeSelectedID);
    if (!selectedStillExists) {
      app.runtimeSelectedID = selectDefault && targets.length ? targets[0].id : "";
      clearRuntimeInspector(status.ready ? "Choose an object from Runtime targets." : "Runtime bridge unavailable.");
    }
    renderRuntimeTargets();
    return status;
  } catch (error) {
    app.runtimeStatus = null;
    renderRuntimeTargets();
    clearRuntimeInspector("Runtime target discovery failed.");
    if (!silent) toast(error.message, true);
    return null;
  }
}

function replicationLabel(replication) {
  const scope = replication?.scope || "server_only";
  const labels = {
    server_only: "Server only",
    replicated: "Replicated",
    initial_only: "Initial only",
    owner_only: "Owner only",
    skip_owner: "Skip owner",
    simulated_only: "Simulated only",
    autonomous_only: "Autonomous only",
    simulated_or_physics: "Simulated / physics",
    initial_or_owner: "Initial / owner",
    custom: "Custom condition",
    replay_or_owner: "Replay / owner",
    replay_only: "Replay only",
    skip_replay: "Skip replay",
    conditional: "Conditional",
  };
  return labels[scope] || scope.replaceAll("_", " ");
}

function replicationBadgeClass(replication) {
  if (replication?.scope === "replicated") return " replicated";
  if (replication?.scope && replication.scope !== "server_only") return " conditional";
  return "";
}

function populateRuntimeClassFilter(classChain) {
  const select = $("#runtime-class-filter");
  const previous = select.value;
  select.replaceChildren();
  const all = document.createElement("option");
  all.value = "";
  all.textContent = "All inherited classes";
  select.append(all);
  for (const className of classChain || []) {
    const option = document.createElement("option");
    option.value = className;
    option.textContent = className;
    select.append(option);
  }
  if ([...select.options].some((option) => option.value === previous)) {
    select.value = previous;
  }
}

function runtimeEditControl() {
  return [
    $("#runtime-edit-select"),
    $("#runtime-edit-input"),
    $("#runtime-edit-value"),
  ].find((control) => !control.classList.contains("hidden"));
}

function validRuntimeIntegerText(value, minimum, maximum) {
  if (!/^[+-]?[0-9]+$/.test(value)) return false;
  try {
    const parsed = BigInt(value);
    return parsed >= BigInt(minimum) && parsed <= BigInt(maximum);
  } catch (_) {
    return false;
  }
}

function validRuntimeNumberText(value, minimum, maximum, bits) {
  if (!/^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$/.test(value)) {
    return false;
  }
  const parsed = Number(value);
  const mantissa = value.split(/[eE]/, 1)[0];
  const textIsZero = !/[1-9]/.test(mantissa);
  if (!Number.isFinite(parsed) ||
      (parsed === 0 && !textIsZero) ||
      parsed < Number(minimum) ||
      parsed > Number(maximum)) {
    return false;
  }
  if (bits === 32) {
    const narrowed = Math.fround(parsed);
    return Number.isFinite(narrowed) &&
      !(narrowed === 0 && parsed !== 0);
  }
  return true;
}

function validBalancedRuntimeText(value) {
  const stack = [];
  let quote = "";
  let escaped = false;
  const closing = { ")": "(", "]": "[", "}": "{" };
  for (const character of value) {
    if (quote) {
      if (escaped) {
        escaped = false;
      } else if (character === "\\") {
        escaped = true;
      } else if (character === quote) {
        quote = "";
      }
      continue;
    }
    if (character === '"' || character === "'") {
      quote = character;
    } else if (character === "(" || character === "[" || character === "{") {
      stack.push(character);
    } else if (closing[character]) {
      if (stack.pop() !== closing[character]) return false;
    }
  }
  return !quote && !escaped && stack.length === 0;
}

function runtimeEditValidationMessage(property, value) {
  const editor = property.editor || {};
  switch (editor.kind) {
    case "boolean":
      return value === "True" || value === "False"
        ? ""
        : "Choose True or False.";
    case "select":
      return (property.enum_values || []).includes(value)
        ? ""
        : "Choose one of the available Unreal enum values.";
    case "integer":
      return validRuntimeIntegerText(value, editor.min, editor.max)
        ? ""
        : `Enter a whole number from ${editor.min} to ${editor.max}.`;
    case "number":
      return validRuntimeNumberText(
        value,
        editor.min,
        editor.max,
        property.type === "FloatProperty" ? 32 : 64,
      )
        ? ""
        : `Enter a finite number from ${editor.min} to ${editor.max}.`;
    case "name":
      return value && !/[\u0000-\u001f\u007f]/u.test(value)
        ? ""
        : "Enter one non-empty Unreal name without control characters.";
    case "string":
      return "";
    case "unreal_text":
      return validBalancedRuntimeText(value)
        ? ""
        : "Quotes, parentheses, brackets, or braces are not balanced.";
    default:
      return "This property does not have a safe editor.";
  }
}

function validateRuntimeEditor() {
  const editing = app.runtimeEditing;
  const control = runtimeEditControl();
  if (!editing || !control) return false;
  const message = runtimeEditValidationMessage(editing.property, control.value);
  control.setCustomValidity(message);
  const help = $("#runtime-edit-help");
  help.textContent = message || editing.editorHelp;
  help.classList.toggle("form-error", Boolean(message));
  $("#runtime-edit-save").disabled = app.runtimeSaving || Boolean(message);
  return !message;
}

function configureRuntimeEditor(property) {
  const select = $("#runtime-edit-select");
  const input = $("#runtime-edit-input");
  const textarea = $("#runtime-edit-value");
  for (const control of [select, input, textarea]) {
    control.classList.add("hidden");
    control.disabled = true;
    control.setCustomValidity("");
  }
  select.replaceChildren();
  input.inputMode = "text";

  const editor = property.editor || {};
  let control = textarea;
  let label = "Unreal exported text value";
  let help = editor.help || "";
  if (editor.kind === "boolean") {
    control = select;
    label = "Boolean value";
    for (const value of ["True", "False"]) {
      const option = document.createElement("option");
      option.value = value;
      option.textContent = value;
      select.append(option);
    }
  } else if (editor.kind === "select") {
    control = select;
    label = "Enum value";
    for (const value of property.enum_values || []) {
      const option = document.createElement("option");
      option.value = value;
      option.textContent = value;
      select.append(option);
    }
  } else if (editor.kind === "integer") {
    control = input;
    label = "Integer value";
    input.inputMode = editor.min?.startsWith("-") ? "text" : "numeric";
    help = `Whole number from ${editor.min} to ${editor.max}.`;
  } else if (editor.kind === "number") {
    control = input;
    label = "Numeric value";
    input.inputMode = "decimal";
    help = `Finite number from ${editor.min} to ${editor.max}. Decimal and scientific notation are accepted.`;
  } else if (editor.kind === "name") {
    control = input;
    label = "Unreal name";
  } else if (editor.kind === "string") {
    label = "String value";
  }
  control.classList.remove("hidden");
  control.disabled = false;
  control.value = property.value;
  $("#runtime-edit-value-label").textContent = label;
  app.runtimeEditing.editorHelp = help;
  validateRuntimeEditor();
  return control;
}

function openRuntimeEditor(property) {
  if (!app.runtimeTarget || !property.editable || typeof property.value !== "string") return;
  app.runtimeEditing = {
    targetID: app.runtimeTarget.target.id,
    property,
    expectedValue: property.value,
  };
  const suffix = property.array_dim > 1 ? `[${property.array_index}]` : "";
  $("#runtime-edit-title").textContent = `${property.name}${suffix}`;
  $("#runtime-edit-meta").textContent = `${property.declaring_class} · ${property.type}`;
  $("#runtime-edit-replication").textContent =
    `${replicationLabel(property.replication)} · condition ${property.replication.condition}. ` +
    (property.replication.scope === "server_only"
      ? "This change remains on the authoritative server instance."
      : "Net dormancy is flushed and ForceNetUpdate is requested; delivery still follows Unreal ownership, relevancy, and replication rules.");
  const dialog = $("#runtime-edit-dialog");
  if (typeof dialog.showModal === "function") dialog.showModal();
  else dialog.setAttribute("open", "");
  configureRuntimeEditor(property).focus();
}

function closeRuntimeEditor() {
  app.runtimeEditing = null;
  const dialog = $("#runtime-edit-dialog");
  if (typeof dialog.close === "function" && dialog.open) dialog.close();
  else dialog.removeAttribute("open");
}

function runtimePropertyMatchesQuery(property, query) {
  if (!query) return true;
  if (`${property.name} ${property.type} ${property.declaring_class}`
      .toLocaleLowerCase()
      .includes(query)) {
    return true;
  }
  return typeof property.value === "string" &&
    property.value.toLocaleLowerCase().includes(query);
}

function renderRuntimeProperties() {
  const view = app.runtimeTarget;
  const container = $("#runtime-properties");
  container.replaceChildren();
  if (!view || !view.target) {
    clearRuntimeInspector();
    return;
  }
  $("#runtime-target-title").textContent = `${runtimeTargetLabel(view.target)} · ${view.target.class}`;
  $("#runtime-refresh-properties").disabled = app.runtimeManualRefreshing;
  populateRuntimeClassFilter(view.class_chain);

  const query = $("#runtime-property-search").value.trim().toLocaleLowerCase();
  const classFilter = $("#runtime-class-filter").value;
  const editableOnly = $("#runtime-editable-only").checked;
  const properties = (view.properties || []).filter((property) => {
    if (classFilter && property.declaring_class !== classFilter) return false;
    if (editableOnly && !property.editable) return false;
    return runtimePropertyMatchesQuery(property, query);
  });
  $("#runtime-property-count").textContent =
    `${properties.length} / ${view.property_count} properties`;
  $("#runtime-empty").classList.toggle("hidden", properties.length > 0);
  $("#runtime-empty").textContent = properties.length
    ? ""
    : "No runtime properties match the current filters.";

  const groups = new Map();
  for (const property of properties) {
    if (!groups.has(property.declaring_class)) groups.set(property.declaring_class, []);
    groups.get(property.declaring_class).push(property);
  }
  for (const className of view.class_chain || []) {
    const classProperties = groups.get(className);
    if (!classProperties?.length) continue;
    const group = document.createElement("section");
    group.className = "runtime-property-group";
    const heading = document.createElement("h3");
    heading.textContent = `${className} · ${classProperties.length}`;
    group.append(heading);

    for (const property of classProperties) {
      const row = document.createElement("div");
      row.className = "runtime-property-row";
      const identity = document.createElement("div");
      identity.className = "runtime-property-name";
      const name = document.createElement("strong");
      const arraySuffix = property.array_dim > 1 ? `[${property.array_index}]` : "";
      name.textContent = `${property.name}${arraySuffix}`;
      const metadata = document.createElement("small");
      metadata.textContent =
        `${property.type} · offset 0x${Number(property.offset).toString(16)} · ${property.flags}`;
      identity.append(name, metadata);

      const value = document.createElement("pre");
      value.className = "runtime-property-value";
      if (typeof property.value === "string") {
        value.textContent = property.value;
      } else {
        value.textContent = "Value unavailable";
        value.classList.add("unavailable");
      }

      const replication = document.createElement("span");
      replication.className = `replication-badge${replicationBadgeClass(property.replication)}`;
      replication.textContent = replicationLabel(property.replication);
      replication.title =
        `Condition: ${property.replication?.condition || "None"} · RepIndex: ${property.replication?.rep_index ?? 0}`;

      const edit = makeButton(
        property.editable && typeof property.value === "string" ? "Edit" : "Read only",
        property.editable ? "secondary compact" : "ghost compact",
        () => openRuntimeEditor(property),
      );
      edit.disabled = !property.editable || typeof property.value !== "string";
      if (edit.disabled) {
        edit.title = property.read_only_reason || "This value cannot be safely imported.";
      }
      row.append(identity, value, replication, edit);
      group.append(row);
    }
    container.append(group);
  }
}

function setRuntimeManualRefresh(active) {
  app.runtimeManualRefreshing = active;
  const button = $("#runtime-refresh-properties");
  button.disabled = active || !app.runtimeSelectedID;
  button.textContent = active ? "Refreshing…" : "Refresh values";
  button.setAttribute("aria-busy", String(active));
}

async function loadRuntimeTarget({ silent = false, manual = false } = {}) {
  if (!app.runtimeSelectedID) {
    clearRuntimeInspector();
    return;
  }
  if (app.runtimeLoading) {
    if (manual) app.runtimeReloadRequested = true;
    return;
  }
  app.runtimeLoading = true;
  if (manual) setRuntimeManualRefresh(true);
  try {
    const view = await api(`/api/runtime/target?id=${encodeURIComponent(app.runtimeSelectedID)}`);
    if (view.target?.id === app.runtimeSelectedID) {
      app.runtimeTarget = view;
      renderRuntimeProperties();
    }
  } catch (error) {
    if (!silent) toast(error.message, true);
    if (error.status === 404 || error.status === 503) {
      await loadRuntimeTargets({ selectDefault: false, silent: true });
    }
  } finally {
    app.runtimeLoading = false;
    if (manual) setRuntimeManualRefresh(false);
    if (app.runtimeReloadRequested) {
      app.runtimeReloadRequested = false;
      loadRuntimeTarget({ manual: true });
    }
  }
}

async function submitRuntimeEdit(event) {
  event.preventDefault();
  const editing = app.runtimeEditing;
  if (!editing) return;
  const control = runtimeEditControl();
  if (!control || !validateRuntimeEditor()) {
    control?.reportValidity();
    return;
  }
  const save = $("#runtime-edit-save");
  app.runtimeSaving = true;
  save.disabled = true;
  try {
    const result = await api("/api/runtime/property", {
      method: "POST",
      body: {
        target_id: editing.targetID,
        declaring_class: editing.property.declaring_class,
        name: editing.property.name,
        array_index: editing.property.array_index,
        expected_value: editing.expectedValue,
        new_value: control.value,
      },
    });
    closeRuntimeEditor();
    toast(`Runtime property applied · ${replicationLabel(result.property.replication)}.`);
    await loadRuntimeTarget({ silent: true });
  } catch (error) {
    toast(error.message, true);
    if (error.status === 409) {
      closeRuntimeEditor();
      await loadRuntimeTarget({ silent: true });
    }
  } finally {
    app.runtimeSaving = false;
    if (app.runtimeEditing) validateRuntimeEditor();
    else save.disabled = false;
  }
}

async function refreshRuntimeLive() {
  if (!runtimePanelActive() || $("#runtime-edit-dialog").open) return;
  await loadRuntimeTargets({ selectDefault: false, silent: true });
  if (app.runtimeSelectedID) await loadRuntimeTarget({ silent: true });
}

function playersPanelActive() {
  return $("#panel-players").classList.contains("active");
}

function playerDate(value) {
  if (!value) return null;
  const date = new Date(value);
  if (Number.isNaN(date.getTime()) || date.getUTCFullYear() < 2000) return null;
  return date;
}

function formatPlayerDate(value, fallback = "Unknown") {
  const date = playerDate(value);
  return date ? date.toLocaleString() : fallback;
}

function formatPlayerDuration(totalSeconds) {
  let seconds = Math.max(0, Math.floor(Number(totalSeconds) || 0));
  const days = Math.floor(seconds / 86400);
  seconds %= 86400;
  const hours = Math.floor(seconds / 3600);
  seconds %= 3600;
  const minutes = Math.floor(seconds / 60);
  seconds %= 60;
  if (days) return `${days}d ${hours}h ${minutes}m`;
  if (hours) return `${hours}h ${minutes}m ${seconds}s`;
  if (minutes) return `${minutes}m ${seconds}s`;
  return `${seconds}s`;
}

function playerRestrictionAvailable() {
  const status = app.players?.restrictions || {};
  return status.server_running === true && status.available === true;
}

function renderPlayerRestrictionStatus() {
  const status = app.players?.restrictions || {};
  const element = $("#player-restriction-status");
  if (status.available) {
    element.textContent = `Server restrictions synced ${formatPlayerDate(status.last_synced_at)}`;
    element.classList.remove("restriction-error");
  } else if (!status.server_running) {
    element.textContent = "Start the game server to change mute or ban state.";
    element.classList.add("restriction-error");
  } else {
    element.textContent = status.error
      ? `Restriction sync unavailable · ${status.error}`
      : "Restriction sync unavailable";
    element.classList.add("restriction-error");
  }
}

function playerMatchesQuery(player, query) {
  if (!query) return true;
  const values = [
    player.playfab_id,
    player.last_nickname,
    ...(Array.isArray(player.nicknames) ? player.nicknames : []),
  ];
  return values.some((value) =>
    typeof value === "string" && value.toLocaleLowerCase().includes(query));
}

function renderPlayerList() {
  const list = $("#player-list");
  list.replaceChildren();
  const players = Array.isArray(app.players?.players) ? app.players.players : [];
  const query = $("#player-search").value.trim().toLocaleLowerCase();
  const filtered = players.filter((player) => playerMatchesQuery(player, query));
  $("#player-count").textContent = query
    ? `${filtered.length} of ${players.length}`
    : `${players.length} player${players.length === 1 ? "" : "s"}`;
  renderPlayerRestrictionStatus();

  if (!filtered.length) {
    const empty = document.createElement("p");
    empty.className = "hint player-list-empty";
    empty.textContent = players.length
      ? "No player matches that PlayFabID or nickname."
      : "No player connections have been recorded yet.";
    list.append(empty);
    return;
  }

  for (const player of filtered) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "player-list-item";
    button.classList.toggle("active", player.playfab_id === app.playerSelectedID);
    button.setAttribute(
      "aria-pressed",
      String(player.playfab_id === app.playerSelectedID),
    );

    const identity = document.createElement("span");
    identity.className = "player-list-identity";
    const nickname = document.createElement("strong");
    nickname.textContent = player.last_nickname || "Unknown nickname";
    const playFabID = document.createElement("code");
    playFabID.textContent = player.playfab_id;
    identity.append(nickname, playFabID);

    const meta = document.createElement("span");
    meta.className = "player-list-meta";
    const joined = document.createElement("small");
    joined.textContent = `Last joined · ${formatPlayerDate(player.last_connected_at)}`;
    const states = document.createElement("span");
    states.className = "player-list-states";
    if (player.connected) {
      const online = document.createElement("i");
      online.className = "player-list-state online";
      online.textContent = "Online";
      states.append(online);
    }
    if (player.muted) {
      const muted = document.createElement("i");
      muted.className = "player-list-state warning";
      muted.textContent = "Muted";
      states.append(muted);
    }
    if (player.banned) {
      const banned = document.createElement("i");
      banned.className = "player-list-state danger";
      banned.textContent = "Banned";
      states.append(banned);
    }
    meta.append(joined, states);
    button.append(identity, meta);
    button.addEventListener("click", async () => {
      if (app.playerSelectedID === player.playfab_id && app.playerDetail) return;
      app.playerSelectedID = player.playfab_id;
      app.playerDetail = null;
      renderPlayerList();
      renderPlayerProfile();
      await loadPlayerDetail();
    });
    list.append(button);
  }
}

function renderPlayerKnownValues(selector, values, emptyText, code = false) {
  const container = $(selector);
  container.replaceChildren();
  if (!Array.isArray(values) || !values.length) {
    const empty = document.createElement("p");
    empty.className = "hint";
    empty.textContent = emptyText;
    container.append(empty);
    return;
  }
  for (const item of values) {
    const row = document.createElement("div");
    row.className = "player-known-value";
    const value = document.createElement(code ? "code" : "strong");
    value.textContent = item.value;
    const seen = document.createElement("small");
    seen.textContent = `Last seen · ${formatPlayerDate(item.last_seen_at)}`;
    row.append(value, seen);
    container.append(row);
  }
}

function renderPlayerComments(comments) {
  const container = $("#player-comments");
  container.replaceChildren();
  const values = Array.isArray(comments) ? comments : [];
  $("#player-comment-count").textContent =
    `${values.length} comment${values.length === 1 ? "" : "s"}`;
  if (!values.length) {
    const empty = document.createElement("p");
    empty.className = "hint";
    empty.textContent = "No administrator comments have been added.";
    container.append(empty);
    return;
  }
  for (const comment of values) {
    const article = document.createElement("article");
    article.className = "player-comment";
    const meta = document.createElement("div");
    meta.className = "player-comment-meta";
    const author = document.createElement("strong");
    author.textContent = comment.author || "Unknown account";
    const time = document.createElement("time");
    time.dateTime = comment.created_at || "";
    time.textContent = formatPlayerDate(comment.created_at);
    meta.append(author, time);
    const body = document.createElement("p");
    body.textContent = comment.body || "";
    article.append(meta, body);
    container.append(article);
  }
}

function renderPlayerLiveDuration() {
  const detail = app.playerDetail;
  if (!detail || detail.playfab_id !== app.playerSelectedID) return;
  let total = Number(detail.total_seconds) || 0;
  if (detail.connected) {
    const generatedAt = playerDate(detail.generated_at);
    if (generatedAt) {
      total += Math.max(0, Math.floor((Date.now() - generatedAt.getTime()) / 1000));
    }
  }
  $("#player-total-time").textContent = formatPlayerDuration(total);
}

function renderPlayerProfile() {
  const detail = app.playerDetail;
  const valid = detail && detail.playfab_id === app.playerSelectedID;
  $("#player-profile-empty").classList.toggle("hidden", Boolean(valid));
  $("#player-profile").classList.toggle("hidden", !valid);
  if (!valid) {
    const heading = $("#player-profile-empty h2");
    const hint = $("#player-profile-empty .hint");
    if (app.playerSelectedID && app.playerDetailLoading) {
      heading.textContent = "Loading player";
      hint.textContent = "Reading connection history and administrator comments.";
    } else if (app.playerSelectedID) {
      heading.textContent = "Player unavailable";
      hint.textContent = "Refresh the player list and select the record again.";
    } else {
      heading.textContent = "Select a player";
      hint.textContent = "Choose a PlayFabID from the history to inspect connection activity, moderation state, known identities, and administrator comments.";
    }
    return;
  }

  $("#player-profile-name").textContent = detail.last_nickname || "Unknown nickname";
  $("#player-profile-id").textContent = detail.playfab_id;
  $("#player-last-connected").textContent =
    formatPlayerDate(detail.last_connected_at, "Not recorded");
  $("#player-current-session").textContent = detail.connected
    ? `Online since ${formatPlayerDate(detail.active_since)}`
    : "Offline";
  renderPlayerLiveDuration();

  const onlineBadge = $("#player-profile-online");
  onlineBadge.textContent = detail.connected ? "Online" : "Offline";
  onlineBadge.className = `player-state-badge${detail.connected ? " online" : ""}`;
  const mutedBadge = $("#player-profile-muted");
  mutedBadge.textContent = detail.muted ? "Muted" : "Not muted";
  mutedBadge.className = `player-state-badge${detail.muted ? " warning" : ""}`;
  const bannedBadge = $("#player-profile-banned");
  bannedBadge.textContent = detail.banned ? "Banned" : "Not banned";
  bannedBadge.className = `player-state-badge${detail.banned ? " danger" : ""}`;

  const moderationAvailable = playerRestrictionAvailable() &&
    !app.playerMutationRunning;
  const mute = $("#player-mute-toggle");
  const ban = $("#player-ban-toggle");
  mute.checked = Boolean(detail.muted);
  ban.checked = Boolean(detail.banned);
  mute.disabled = !moderationAvailable;
  ban.disabled = !moderationAvailable;
  $("#player-moderation-hint").textContent = moderationAvailable
    ? "Changes are executed and verified against the running server."
    : (app.players?.restrictions?.error || "Server restriction state is unavailable.");

  renderPlayerKnownValues(
    "#player-nicknames",
    detail.nicknames,
    "No nickname has been recorded.",
  );
  renderPlayerKnownValues(
    "#player-addresses",
    detail.addresses,
    "No IP address has been correlated from the game log.",
    true,
  );
  renderPlayerComments(detail.comments);
  $("#player-comment-body").disabled = app.playerMutationRunning;
  $("#player-comment-submit").disabled = app.playerMutationRunning;
}

async function loadPlayerDetail({ silent = false } = {}) {
  const playFabID = app.playerSelectedID;
  if (!playFabID) {
    app.playerDetail = null;
    renderPlayerProfile();
    return;
  }
  const requestSequence = ++app.playerDetailRequestSequence;
  app.playerDetailLoading = true;
  renderPlayerProfile();
  try {
    const detail = await api(
      `/api/players/detail?playfab_id=${encodeURIComponent(playFabID)}`,
    );
    if (requestSequence === app.playerDetailRequestSequence &&
        app.playerSelectedID === playFabID) {
      app.playerDetail = detail;
      renderPlayerProfile();
    }
  } catch (error) {
    if (requestSequence === app.playerDetailRequestSequence &&
        app.playerSelectedID === playFabID) {
      app.playerDetail = null;
      renderPlayerProfile();
    }
    if (!silent) toast(error.message, true);
  } finally {
    if (requestSequence === app.playerDetailRequestSequence) {
      app.playerDetailLoading = false;
      renderPlayerProfile();
    }
  }
}

async function loadPlayers({ silent = false } = {}) {
  if (app.playersLoading) {
    app.playersReloadRequested = true;
    return;
  }
  app.playersLoading = true;
  $("#player-refresh").disabled = true;
  try {
    const view = await api("/api/players");
    app.players = view;
    if (Number.isSafeInteger(view.revision)) app.playerRevision = view.revision;
    const players = Array.isArray(view.players) ? view.players : [];
    if (!players.some((player) => player.playfab_id === app.playerSelectedID)) {
      app.playerSelectedID = players[0]?.playfab_id || "";
      app.playerDetail = null;
    }
    renderPlayerList();
    if (app.playerSelectedID) {
      await loadPlayerDetail({ silent });
    } else {
      app.playerDetail = null;
      renderPlayerProfile();
    }
  } catch (error) {
    if (!silent) toast(error.message, true);
  } finally {
    app.playersLoading = false;
    $("#player-refresh").disabled = false;
    if (app.playersReloadRequested) {
      app.playersReloadRequested = false;
      loadPlayers({ silent: true });
    }
  }
}

async function setPlayerRestriction(restriction, enabled) {
  const detail = app.playerDetail;
  if (!detail || app.playerMutationRunning) return;
  if (restriction === "ban" && enabled &&
      !confirm(`Ban ${detail.last_nickname || detail.playfab_id} permanently until unbanned?`)) {
    renderPlayerProfile();
    return;
  }
  app.playerMutationRunning = true;
  renderPlayerProfile();
  try {
    app.playerDetail = await api("/api/players/restriction", {
      method: "POST",
      body: {
        playfab_id: detail.playfab_id,
        restriction,
        enabled,
      },
    });
    toast(`${restriction === "ban" ? "Ban" : "Mute"} ${enabled ? "enabled" : "removed"} for ${detail.playfab_id}.`);
    await loadPlayers({ silent: true });
  } catch (error) {
    toast(error.message, true);
  } finally {
    app.playerMutationRunning = false;
    renderPlayerProfile();
  }
}

async function submitPlayerComment(event) {
  event.preventDefault();
  const detail = app.playerDetail;
  const body = $("#player-comment-body");
  if (!detail || app.playerMutationRunning || !body.value.trim()) return;
  app.playerMutationRunning = true;
  renderPlayerProfile();
  try {
    app.playerDetail = await api("/api/players/comments", {
      method: "POST",
      body: {
        playfab_id: detail.playfab_id,
        body: body.value,
      },
    });
    body.value = "";
    renderPlayerProfile();
    toast("Player comment added.");
  } catch (error) {
    toast(error.message, true);
  } finally {
    app.playerMutationRunning = false;
    renderPlayerProfile();
  }
}

async function loadConfig() {
  const file = $("#config-file").value;
  try {
    app.config = await api(`/api/config?file=${encodeURIComponent(file)}`);
    renderConfig();
  } catch (error) {
    toast(error.message, true);
  }
}

function makeButton(text, className, action) {
  const button = document.createElement("button");
  button.type = "button";
  button.textContent = text;
  button.className = className;
  button.addEventListener("click", action);
  return button;
}

async function mutateConfig(mutation) {
  try {
    app.config = await api("/api/config/mutate", {
      method: "POST",
      body: {
        file: app.config.file,
        revision: app.config.revision,
        line: -1,
        section_line: -1,
        section: "",
        key: "",
        value: "",
        enabled: true,
        ...mutation,
      },
    });
    renderConfig();
    toast("Configuration saved.");
  } catch (error) {
    toast(error.message, true);
    if (error.message.includes("reload") || error.message.includes("lifecycle")) await loadConfig();
  }
}

function renderConfig() {
  const target = $("#config-sections");
  target.replaceChildren();
  if (!app.config) return;
  $("#config-stage").textContent = app.config.staged ? "Staged" : "Active";
  $("#config-stage").classList.toggle("staged", app.config.staged);
  $("#discard-config").disabled = !app.config.staged;

  for (const section of app.config.sections) {
    const card = document.createElement("section");
    card.className = "config-section";
    card.classList.toggle("disabled", section.enabled === false);
    const head = document.createElement("div");
    head.className = "section-head";
    const sectionInput = document.createElement("input");
    sectionInput.value = section.name;
    const globalSection = section.line < 0 && !section.id &&
      section.name === "(entries before first section)";
    sectionInput.disabled = globalSection;
    sectionInput.autocapitalize = "none";
    sectionInput.spellcheck = false;
    sectionInput.setAttribute("aria-label", "Section name");
    head.append(sectionInput);
    if (!globalSection) {
      head.append(
        makeButton("Rename", "secondary compact", () =>
          mutateConfig({
            action: "rename_section",
            line: section.line,
            section_id: section.id || "",
            section: sectionInput.value,
          })),
        makeButton(
          section.enabled === false ? "Enable section" : "Disable section",
          section.enabled === false ? "primary compact" : "ghost compact",
          () => mutateConfig({
            action: "set_section_enabled",
            line: section.line,
            section_id: section.id || "",
            section: section.name,
            enabled: section.enabled === false,
          }),
        ),
        makeButton("Remove", "danger compact", () => {
          if (confirm(`Remove [${section.name}] and all active or disabled items in that section?`)) {
            mutateConfig({
              action: "remove_section",
              line: section.line,
              section_id: section.id || "",
              section: section.name,
            });
          }
        }),
      );
    }
    card.append(head);

    const entries = document.createElement("div");
    entries.className = "entry-list";
    for (const entry of section.entries) {
      const row = document.createElement("div");
      row.className = "entry-row";
      row.classList.toggle("disabled", !entry.enabled);
      const key = document.createElement("input");
      key.value = entry.key;
      key.autocapitalize = "none";
      key.spellcheck = false;
      key.setAttribute("aria-label", "Entry key");
      const value = document.createElement("input");
      value.value = entry.value;
      value.autocapitalize = "none";
      value.spellcheck = false;
      value.setAttribute("aria-label", "Entry value");
      row.append(
        key,
        value,
        makeButton(entry.enabled ? "Disable" : "Enable", entry.enabled ? "ghost compact" : "primary compact", () =>
          mutateConfig({
            action: "set_entry_enabled",
            line: entry.line,
            entry_id: entry.id || "",
            section: section.name,
            key: entry.key,
            enabled: !entry.enabled,
          })),
        makeButton("Save", "secondary compact", () =>
          mutateConfig({
            action: "set_entry",
            line: entry.line,
            entry_id: entry.id || "",
            section: section.name,
            key: key.value,
            value: value.value,
          })),
        makeButton("Remove", "danger compact", () => {
          if (confirm(`Remove ${entry.key}?`)) {
            mutateConfig({
              action: "remove_entry",
              line: entry.line,
              entry_id: entry.id || "",
              section: section.name,
              key: entry.key,
            });
          }
        }),
      );
      entries.append(row);
    }
    card.append(entries);

    const add = document.createElement("form");
    add.className = "add-entry";
    const newKey = document.createElement("input");
    newKey.placeholder = "New key";
    newKey.required = true;
    newKey.autocapitalize = "none";
    newKey.spellcheck = false;
    const newValue = document.createElement("input");
    newValue.placeholder = "Value";
    newValue.autocapitalize = "none";
    newValue.spellcheck = false;
    const submit = document.createElement("button");
    submit.type = "submit";
    submit.className = "primary compact";
    submit.textContent = "Add item";
    add.append(newKey, newValue, submit);
    add.addEventListener("submit", (event) => {
      event.preventDefault();
      mutateConfig({
        action: "add_entry",
        section_line: section.line,
        section_id: section.id || "",
        section: section.name,
        key: newKey.value,
        value: newValue.value,
      });
    });
    card.append(add);
    target.append(card);
  }
}

function modDisplayName(item) {
  return item && item.name ? item.name : `Resource ID ${item ? item.id : "—"}`;
}

function formatModDate(seconds) {
  if (!Number.isFinite(seconds) || seconds <= 0) return "";
  return new Date(seconds * 1000).toLocaleString();
}

function formatBrowserDate(value) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  let zone = "";
  try {
    zone = Intl.DateTimeFormat().resolvedOptions().timeZone || "";
  } catch (_) {}
  let formatted = "";
  try {
    formatted = date.toLocaleString(undefined, {
      dateStyle: "medium",
      timeStyle: "medium",
    });
  } catch (_) {
    formatted = date.toLocaleString();
  }
  return zone ? `${formatted} (${zone})` : formatted;
}

function renderModRefresh(refresh) {
  const minutes = validModRefreshMinutes(refresh && refresh.interval_minutes) || 5;
  $("#mods-refresh-minutes").value = String(minutes);
  $("#mods-restart-on-update").checked = Boolean(refresh && refresh.restart_on_update);

  const status = $("#mods-refresh-status");
  const lastSuccess = formatBrowserDate(refresh && refresh.last_success_at);
  const nextRefresh = formatBrowserDate(refresh && refresh.next_refresh_at);
  const failed = Boolean(refresh && refresh.last_error);
  const parts = [`Server interval: ${minutes} min`];
  parts.push(lastSuccess
    ? `Last successful refresh: ${lastSuccess}`
    : "No successful refresh yet");
  if (refresh && refresh.refreshing) {
    parts.push("Refreshing now");
  } else if (nextRefresh) {
    parts.push(`${failed ? "Next retry" : "Next refresh"}: ${nextRefresh}`);
  }
  if (failed) parts.push(`Last attempt failed: ${refresh.last_error}`);
  if (refresh && refresh.restart_scheduled && refresh.restart_at) {
    const restartAt = formatBrowserDate(refresh.restart_at);
    const modIDs = Array.isArray(refresh.restart_mod_ids)
      ? refresh.restart_mod_ids.join(", ")
      : "";
    parts.push(
      `Update restart scheduled: ${restartAt || refresh.restart_at}${modIDs ? ` · mods ${modIDs}` : ""}`,
    );
  }
  status.textContent = parts.join(" · ");
  status.classList.toggle("error", failed);
}

function renderModIOSettings(settings) {
  const configured = Boolean(settings && settings.api_key_configured);
  const status = $("#modio-key-status");
  status.textContent = configured
    ? `${settings.game_name || "MORDHAU"} · game ${settings.game_id}`
    : "Not configured";
  status.classList.toggle("connected", configured);
  $("#modio-api-base").value = settings && settings.api_base
    ? settings.api_base
    : "https://api.mod.io/v1";
  $("#modio-api-key").placeholder = configured
    ? "Saved · enter only to replace"
    : "32-character key";
  $("#modio-clear").disabled = !configured;
  const restart = $("#mods-restart-on-update");
  restart.disabled = !configured;
  if (!configured) restart.checked = false;
  $("#mods-restart-on-update-hint").textContent = configured
    ? "When an active mod publishes a new modfile, players receive a 10-minute countdown before a managed restart."
    : "Save a valid API key to enable this option. It is off by default.";
}

function appendConfiguredModDependencies(details, configured, configuredByID) {
  const section = document.createElement("div");
  section.className = "mod-dependencies";

  const dependencies = Array.isArray(configured.dependencies)
    ? configured.dependencies
    : [];
  const unresolved = new Set(
    Array.isArray(configured.unresolved_dependencies)
      ? configured.unresolved_dependencies
      : [],
  );
  const heading = document.createElement("strong");
  heading.className = "mod-dependencies-heading";
  heading.textContent = configured.dependencies_checked
    ? `Dependencies (${dependencies.length})`
    : "Dependencies";
  section.append(heading);

  if (!configured.metadata) {
    const note = document.createElement("p");
    note.className = "mod-dependency-note";
    note.textContent = "Unavailable — mod.io metadata was not returned for this Resource ID.";
    section.append(note);
    details.append(section);
    return;
  }

  if (configured.dependency_error) {
    const warning = document.createElement("p");
    warning.className = configured.enabled
      ? "mod-dependency-warning"
      : "mod-dependency-note";
    warning.textContent = `Dependency check failed: ${configured.dependency_error}`;
    section.append(warning);
    details.append(section);
    return;
  }

  if (!configured.dependencies_checked) {
    const note = document.createElement("p");
    note.className = "mod-dependency-note";
    note.textContent = "Unavailable — configure a valid mod.io API key to inspect dependencies.";
    section.append(note);
    details.append(section);
    return;
  }

  if (!dependencies.length) {
    const note = document.createElement("p");
    note.className = "mod-dependency-note";
    note.textContent = "None reported by mod.io.";
    section.append(note);
    details.append(section);
    return;
  }

  if (configured.enabled && unresolved.size) {
    const warning = document.createElement("p");
    warning.className = "mod-dependency-warning";
    const ids = [...unresolved].map((id) => `Mods=${id}`).join(", ");
    warning.textContent = `Warning: ${unresolved.size} required dependenc${unresolved.size === 1 ? "y is" : "ies are"} not enabled: ${ids}`;
    section.append(warning);
  }

  const list = document.createElement("ul");
  list.className = "dependency-list configured-dependency-list";
  for (const dependency of dependencies) {
    const item = document.createElement("li");
    const configuredDependency = configuredByID.get(dependency.id);
    const enabled = Boolean(configuredDependency && configuredDependency.enabled);
    const shouldWarn = configured.enabled && unresolved.has(dependency.id);
    item.classList.toggle("unresolved", shouldWarn);

    const identity = document.createElement("span");
    identity.className = "dependency-identity";
    const name = document.createElement("span");
    name.textContent = modDisplayName(dependency);
    const id = document.createElement("code");
    id.textContent = `Mods=${dependency.id}`;
    identity.append(name, id);

    const status = document.createElement("span");
    status.className = `dependency-status${enabled ? " resolved" : shouldWarn ? " unresolved" : ""}`;
    status.textContent = enabled
      ? "enabled"
      : configuredDependency
        ? "disabled"
        : "not configured";
    item.append(identity, status);
    list.append(item);
  }
  section.append(list);
  details.append(section);
}

function renderConfiguredMods(data) {
  if (Number.isSafeInteger(data.revision)) app.modRevision = data.revision;
  renderModRefresh(data.refresh || {});
  renderModIOSettings(data.settings || {});
  const stage = $("#mods-config-stage");
  stage.textContent = data.config_staged ? "Staged" : "Active";
  stage.classList.toggle("staged", Boolean(data.config_staged));

  const apiError = $("#mods-api-error");
  apiError.classList.toggle("hidden", !data.api_error);
  apiError.textContent = data.api_error
    ? `Metadata lookup failed: ${data.api_error}`
    : "";

  const invalid = $("#mods-invalid-warning");
  invalid.classList.toggle("hidden", !data.invalid_entries);
  invalid.textContent = data.invalid_entries
    ? `${data.invalid_entries} invalid Mods= entr${data.invalid_entries === 1 ? "y was" : "ies were"} found. Use INI configuration to correct it.`
    : "";

  const list = $("#configured-mods");
  list.replaceChildren();
  const mods = Array.isArray(data.mods) ? data.mods : [];
  const configuredByID = new Map(mods.map((item) => [item.id, item]));
  if (!mods.length) {
    const empty = document.createElement("p");
    empty.className = "hint";
    empty.textContent = "No Mods= entries are configured in the MORDHAU game session section.";
    list.append(empty);
    return;
  }

  for (const configured of mods) {
    const metadata = configured.metadata;
    const row = document.createElement("div");
    row.className = "mod-row";
    row.classList.toggle("disabled", !configured.enabled);

    const details = document.createElement("div");
    details.className = "mod-details";
    const title = document.createElement("div");
    title.className = "mod-title";
    const name = document.createElement("strong");
    name.textContent = metadata ? modDisplayName(metadata) : `Resource ID ${configured.id}`;
    const id = document.createElement("span");
    id.className = "mod-id";
    id.textContent = `Mods=${configured.id}`;
    title.append(name, id);
    details.append(title);

    if (metadata && metadata.summary) {
      const summary = document.createElement("p");
      summary.className = "mod-summary";
      summary.textContent = metadata.summary;
      summary.title = metadata.summary;
      details.append(summary);
    }
    const meta = document.createElement("div");
    meta.className = "mod-meta";
    const fields = [];
    if (metadata && metadata.modfile && metadata.modfile.version) {
      fields.push(`version ${metadata.modfile.version}`);
    }
    if (metadata && metadata.date_updated) {
      fields.push(`updated ${formatModDate(metadata.date_updated)}`);
    }
    if (configured.occurrences > 1) fields.push(`${configured.occurrences} entries`);
    fields.push(configured.enabled ? "enabled" : "disabled");
    meta.textContent = fields.join(" · ");
    details.append(meta);
    appendConfiguredModDependencies(details, configured, configuredByID);

    const link = document.createElement("a");
    link.className = "secondary-link";
    link.textContent = "mod.io";
    link.target = "_blank";
    link.rel = "noopener noreferrer";
    if (metadata && metadata.name_id) {
      link.href = `https://mod.io/g/mordhau/m/${encodeURIComponent(metadata.name_id)}`;
    } else {
      link.href = `https://mod.io/search/mods/${configured.id}`;
    }

    const toggle = makeButton(
      configured.enabled ? "Disable" : "Enable",
      configured.enabled ? "ghost compact" : "primary compact",
      async () => {
        try {
          const result = await api("/api/mods/enabled", {
            method: "POST",
            body: { id: configured.id, enabled: !configured.enabled },
          });
          toast(`${configured.id} ${configured.enabled ? "disabled" : "enabled"}${result.staged ? " in staged Game.ini" : ""}.`);
          await Promise.all([loadMods(), loadConfig()]);
        } catch (error) {
          toast(error.message, true);
        }
      },
    );
    const remove = makeButton("Remove", "danger compact", async () => {
      if (!confirm(`Remove every Mods=${configured.id} entry? Shared dependencies are not removed automatically.`)) return;
      try {
        const result = await api("/api/mods/remove", {
          method: "POST",
          body: { id: configured.id },
        });
        toast(`Mods=${configured.id} removed${result.staged ? " from staged Game.ini" : ""}.`);
        await Promise.all([loadMods(), loadConfig()]);
      } catch (error) {
        toast(error.message, true);
      }
    });
    row.append(details, link, toggle, remove);
    list.append(row);
  }
}

async function loadMods() {
  if (app.modsLoading) {
    app.modsReloadRequested = true;
    return;
  }
  app.modsLoading = true;
  try {
    const data = await api("/api/mods");
    renderConfiguredMods(data);
  } catch (error) {
    toast(error.message, true);
  } finally {
    app.modsLoading = false;
    if (app.modsReloadRequested) {
      app.modsReloadRequested = false;
      loadMods();
    }
  }
}

const customPakCurrentLabels = {
  active: "Active now",
  inactive: "Inactive now",
  uploaded: "Uploaded",
};

const customPakPendingLabels = {
  install: "Install on next start",
  activate: "Activate on next start",
  deactivate: "Deactivate on next start",
  delete: "Delete on next start",
};

function customPakModifiedAt(value) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "unknown time" : date.toLocaleString();
}

function renderCustomPaks(data) {
  const view = data && typeof data === "object" ? data : {};
  const items = Array.isArray(view.items) ? view.items : [];
  app.customPaks = view;

  const pendingCount = Number.isSafeInteger(view.pending_count)
    ? view.pending_count
    : items.filter((item) => item && item.pending_action).length;
  const pending = Boolean(view.pending_changes || pendingCount);
  const stage = $("#custompak-stage");
  stage.textContent = pending ? `${pendingCount} staged` : "No staged changes";
  stage.classList.toggle("staged", pending);

  const managedPackages = Number.isSafeInteger(view.managed_packages)
    ? view.managed_packages
    : 0;
  const summary = $("#custompak-summary");
  const application = view.server_running
    ? "The running game server is unchanged."
    : "Changes will be applied before the next managed launch.";
  const managedText = managedPackages
    ? ` ${managedPackages} project-managed package${managedPackages === 1 ? " is" : "s are"} visible and protected from deactivation and deletion.`
    : "";
  summary.textContent = pending
    ? `${pendingCount} change${pendingCount === 1 ? "" : "s"} staged for the next managed start or restart. ${application}${managedText}`
    : `No CustomPak changes are staged. ${application}${managedText}`;

  const limit = Number(view.max_upload_bytes);
  const limitLabel = $("#custompak-upload-limit");
  limitLabel.textContent = Number.isFinite(limit) && limit > 0
    ? `Available upload limit: ${bytes(limit)}`
    : "No upload capacity available";
  $("#custompak-browse").disabled = Boolean(app.customPakUploadXHR) ||
    !Number.isFinite(limit) || limit < 1;

  const list = $("#custompak-list");
  list.replaceChildren();
  if (!items.length) {
    const empty = document.createElement("p");
    empty.className = "hint";
    empty.textContent = "No CustomPaks were found.";
    list.append(empty);
    return;
  }

  for (const item of items) {
    if (!item || typeof item.name !== "string") continue;
    const row = document.createElement("div");
    row.className = "custompak-row";
    row.classList.toggle("pending-delete", item.pending_action === "delete");
    row.classList.toggle("managed", Boolean(item.managed));

    const details = document.createElement("div");
    details.className = "custompak-details";
    const title = document.createElement("div");
    title.className = "custompak-title";
    const name = document.createElement("strong");
    name.textContent = item.name;
    const status = document.createElement("span");
    status.className = "custompak-status";
    if (item.managed) {
      status.classList.add("managed");
      status.textContent = "Project managed";
    } else {
      if (item.pending_action) status.classList.add("pending");
      if (item.pending_action === "delete") status.classList.add("delete");
      status.textContent = customPakPendingLabels[item.pending_action] ||
        customPakCurrentLabels[item.current_state] ||
        "Stored";
    }
    title.append(name, status);
    if (item.managed && item.pending_action) {
      const pendingStatus = document.createElement("span");
      pendingStatus.className = "custompak-status pending";
      pendingStatus.textContent = customPakPendingLabels[item.pending_action] ||
        "Pending";
      title.append(pendingStatus);
    }

    const meta = document.createElement("div");
    meta.className = "custompak-meta";
    const current = customPakCurrentLabels[item.current_state] || item.current_state || "Stored";
    const component = item.managed && item.managed_component
      ? ` · component ${item.managed_component}`
      : "";
    meta.textContent = `${current}${component} · ${bytes(Number(item.size))} · modified ${customPakModifiedAt(item.modified_at)}`;
    details.append(title, meta);

    const toggle = document.createElement("label");
    toggle.className = "custompak-switch";
    const checkbox = document.createElement("input");
    checkbox.type = "checkbox";
    checkbox.checked = Boolean(item.enabled);
    checkbox.disabled = item.pending_action === "delete" ||
      (Boolean(item.managed) && checkbox.checked);
    checkbox.setAttribute(
      "aria-label",
      item.managed
        ? `Restore ${item.name} to active on the next server start`
        : `Set ${item.name} active on the next server start`,
    );
    if (item.managed && checkbox.checked) {
      checkbox.title = "Project-managed PAKs cannot be deactivated.";
    }
    const toggleText = document.createElement("span");
    toggleText.textContent = item.managed
      ? (checkbox.checked ? "Active · protected" : "Activate")
      : (checkbox.checked ? "Active" : "Inactive");
    checkbox.addEventListener("change", async () => {
      const enabled = checkbox.checked;
      checkbox.disabled = true;
      try {
        const updated = await api("/api/custompaks/enabled", {
          method: "POST",
          body: { name: item.name, enabled },
        });
        renderCustomPaks(updated);
        toast(`${item.name} will be ${enabled ? "active" : "inactive"} after the next managed start or restart.`);
      } catch (error) {
        checkbox.checked = !enabled;
        checkbox.disabled = false;
        toggleText.textContent = item.managed
          ? (checkbox.checked ? "Active · protected" : "Activate")
          : (checkbox.checked ? "Active" : "Inactive");
        toast(error.message, true);
      }
    });
    toggle.append(checkbox, toggleText);

    let removal;
    if (item.managed) {
      removal = makeButton("Protected", "ghost compact", () => {});
      removal.disabled = true;
      removal.title = "Project-managed PAKs cannot be deleted.";
    } else if (item.pending_action === "delete") {
      removal = makeButton("Cancel delete", "ghost compact", async () => {
        try {
          const updated = await api("/api/custompaks/delete/cancel", {
            method: "POST",
            body: { name: item.name },
          });
          renderCustomPaks(updated);
          toast(`Deletion canceled for ${item.name}.`);
        } catch (error) {
          toast(error.message, true);
        }
      });
    } else {
      removal = makeButton("Delete", "danger compact", async () => {
        if (!confirm(`Delete ${item.name} at the next managed server start or restart?`)) return;
        try {
          const updated = await api("/api/custompaks/delete", {
            method: "POST",
            body: { name: item.name },
          });
          renderCustomPaks(updated);
          toast(`${item.name} is staged for deletion.`);
        } catch (error) {
          toast(error.message, true);
        }
      });
    }
    row.append(details, toggle, removal);
    list.append(row);
  }
}

async function loadCustomPaks({ silent = false } = {}) {
  if (app.customPaksLoading) {
    app.customPaksReloadRequested = true;
    return;
  }
  app.customPaksLoading = true;
  const refresh = $("#custompak-refresh");
  if (refresh) refresh.disabled = true;
  try {
    renderCustomPaks(await api("/api/custompaks"));
  } catch (error) {
    if (!silent) toast(error.message, true);
  } finally {
    app.customPaksLoading = false;
    if (refresh) refresh.disabled = false;
    if (app.customPaksReloadRequested) {
      app.customPaksReloadRequested = false;
      loadCustomPaks({ silent });
    }
  }
}

function setCustomPakUploading(uploading, filename = "") {
  $("#custompak-dropzone").classList.toggle("uploading", uploading);
  $("#custompak-upload-progress").classList.toggle("hidden", !uploading);
  $("#custompak-browse").disabled = uploading ||
    !app.customPaks || Number(app.customPaks.max_upload_bytes) < 1;
  if (uploading) {
    $("#custompak-progress").value = 0;
    $("#custompak-progress-text").textContent = `Uploading ${filename}`;
  }
  if (!uploading) $("#custompak-file").value = "";
}

function customPakUploadError(xhr) {
  let message = xhr.status
    ? `${xhr.status} ${xhr.statusText || "Upload failed"}`
    : "The upload connection failed.";
  try {
    const response = JSON.parse(xhr.responseText || "{}");
    if (response.error) message = response.error;
  } catch (_) {}
  return message;
}

function uploadCustomPak(file) {
  if (!file) return;
  if (app.customPakUploadXHR) {
    toast("Another CustomPak upload is already in progress.", true);
    return;
  }
  if (!/\.pak$/iu.test(file.name)) {
    toast("Choose a file whose name ends in .pak.", true);
    return;
  }
  const limit = Number(app.customPaks && app.customPaks.max_upload_bytes);
  if (!Number.isFinite(limit) || limit < 1) {
    toast("No CustomPak upload capacity is currently available.", true);
    return;
  }
  if (file.size > limit) {
    toast(`${file.name} is ${bytes(file.size)}; the current upload limit is ${bytes(limit)}.`, true);
    return;
  }

  const form = new FormData();
  form.append("file", file, file.name);
  const xhr = new XMLHttpRequest();
  app.customPakUploadXHR = xhr;
  setCustomPakUploading(true, file.name);
  xhr.open("POST", "/api/custompaks/upload");
  xhr.withCredentials = true;
  xhr.setRequestHeader("X-CSRF-Token", app.csrf);
  xhr.upload.addEventListener("progress", (event) => {
    if (!event.lengthComputable) {
      $("#custompak-progress").removeAttribute("value");
      $("#custompak-progress-text").textContent = `Uploading ${file.name}`;
      return;
    }
    const percent = Math.min(100, (event.loaded / event.total) * 100);
    $("#custompak-progress").value = percent;
    $("#custompak-progress-text").textContent =
      `${file.name} · ${percent.toFixed(1)}% · ${bytes(event.loaded)} / ${bytes(event.total)}`;
  });
  xhr.addEventListener("load", () => {
    app.customPakUploadXHR = null;
    setCustomPakUploading(false);
    if (xhr.status === 401) {
      location.href = "/login";
      return;
    }
    if (xhr.status < 200 || xhr.status >= 300) {
      toast(customPakUploadError(xhr), true);
      loadCustomPaks({ silent: true });
      return;
    }
    try {
      renderCustomPaks(JSON.parse(xhr.responseText));
      toast(`${file.name} uploaded and staged as active.`);
    } catch (_) {
      toast("The PAK was uploaded, but the returned package list was invalid.", true);
      loadCustomPaks({ silent: true });
    }
  });
  xhr.addEventListener("error", () => {
    app.customPakUploadXHR = null;
    setCustomPakUploading(false);
    toast("The CustomPak upload connection failed.", true);
    loadCustomPaks({ silent: true });
  });
  xhr.addEventListener("abort", () => {
    app.customPakUploadXHR = null;
    setCustomPakUploading(false);
    toast("CustomPak upload canceled.");
    loadCustomPaks({ silent: true });
  });
  xhr.send(form);
}

function renderModPlan(plan) {
  const target = $("#mod-plan");
  target.replaceChildren();
  target.classList.remove("hidden");

  const heading = document.createElement("h3");
  heading.textContent = `${modDisplayName(plan.target)} · Mods=${plan.target.id}`;
  target.append(heading);

  const note = document.createElement("p");
  note.className = "hint";
  if (plan.dependencies_checked) {
    note.textContent = plan.dependencies.length
      ? `${plan.dependencies.length} recursive dependenc${plan.dependencies.length === 1 ? "y" : "ies"} will be added first.`
      : "mod.io reports no dependencies for this mod.";
  } else {
    note.textContent = "Dependencies were not checked because no API key is configured. Only this numeric Resource ID will be added.";
  }
  target.append(note);

  if (plan.dependencies.length) {
    const list = document.createElement("ul");
    list.className = "dependency-list";
    for (const dependency of plan.dependencies) {
      const item = document.createElement("li");
      const name = document.createElement("span");
      name.textContent = modDisplayName(dependency);
      const id = document.createElement("code");
      id.textContent = `Mods=${dependency.id}`;
      item.append(name, id);
      list.append(item);
    }
    target.append(list);
  }
  const add = $("#mod-plan-add");
  add.textContent = plan.dependencies_checked
    ? "Add mod and dependencies to Game.ini"
    : "Add this Resource ID only";
  add.classList.remove("hidden");
}

async function inspectMod(reference) {
  const plan = await api("/api/mods/plan", {
    method: "POST",
    body: { reference },
  });
  if (!plan || !plan.target || !Number.isInteger(plan.target.id)) {
    throw new Error("mod.io returned an invalid install plan.");
  }
  plan.dependencies = Array.isArray(plan.dependencies) ? plan.dependencies : [];
  app.modPlan = plan;
  app.modPlanReference = reference;
  renderModPlan(plan);
}

async function loadAccounts() {
  try {
    const data = await api("/api/accounts");
    renderAccounts(data.accounts);
  } catch (error) {
    toast(error.message, true);
  }
}

function renderAccounts(accounts) {
  const list = $("#account-list");
  list.replaceChildren();
  for (const account of accounts) {
    const row = document.createElement("div");
    row.className = "list-row";
    const username = document.createElement("input");
    username.value = account.username;
    username.autocapitalize = "none";
    username.spellcheck = false;
    username.setAttribute("aria-label", `Username for ${account.username}`);
    const password = document.createElement("input");
    password.type = "password";
    password.placeholder = "New password (optional)";
    password.autocomplete = "new-password";
    const save = makeButton("Save", "secondary compact", async () => {
      try {
        await api("/api/accounts/edit", {
          method: "POST",
          body: { old_username: account.username, username: username.value, password: password.value },
        });
        toast("Account updated. Its sessions were signed out.");
        if (account.username === app.username) {
          location.href = "/login";
          return;
        }
        loadAccounts();
      } catch (error) {
        toast(error.message, true);
      }
    });
    const remove = makeButton("Delete", "danger compact", async () => {
      if (!confirm(`Delete account ${account.username}?`)) return;
      try {
        await api("/api/accounts/delete", { method: "POST", body: { username: account.username } });
        toast("Account deleted.");
        if (account.username === app.username) {
          location.href = "/login";
          return;
        }
        loadAccounts();
      } catch (error) {
        toast(error.message, true);
      }
    });
    remove.disabled = accounts.length < 2;
    row.append(username, password, save, remove);
    list.append(row);
  }
}

async function loadAccess() {
  try {
    const data = await api("/api/access");
    $("#base-policy").value = data.config.base_policy;
    $("#current-ip").textContent = `Current client address: ${data.current_ip}`;
    renderAccessRules(data.config.rules);
  } catch (error) {
    toast(error.message, true);
  }
}

async function loadServices() {
  try {
    const settings = await api("/api/services");
    $("#mordhau-service-mode").value = settings.mordhau_automatic ? "automatic" : "manual";
    $("#web-service-mode").value = settings.web_automatic ? "automatic" : "manual";
    $("#web-service-port").value = settings.web_port;
    $("#start-map").value = settings.start_map || "";
    const ports = settings.server_ports && typeof settings.server_ports === "object"
      ? settings.server_ports
      : {};
    $("#game-port").value = Number.isInteger(ports.game) ? ports.game : 7777;
    $("#rcon-port").value = Number.isInteger(ports.rcon) ? ports.rcon : 7778;
    $("#beacon-port").value = Number.isInteger(ports.beacon) ? ports.beacon : 15000;
    $("#query-port").value = Number.isInteger(ports.query) ? ports.query : 27015;
  } catch (error) {
    toast(error.message, true);
  }
}

async function setServiceMode(service, value) {
  try {
    await api("/api/services/mode", {
      method: "POST",
      body: { service, automatic: value === "automatic" },
    });
    toast(`${service === "web" ? "Web" : "MORDHAU"} boot mode saved.`);
    loadServices();
  } catch (error) {
    toast(error.message, true);
    loadServices();
  }
}

function renderAccessRules(rules) {
  rules = Array.isArray(rules) ? rules : [];
  const list = $("#access-rules");
  list.replaceChildren();
  if (!rules.length) {
    const empty = document.createElement("p");
    empty.className = "hint";
    empty.textContent = "No explicit network rules.";
    list.append(empty);
    return;
  }
  for (const rule of rules) {
    const row = document.createElement("div");
    row.className = "list-row access-rule-row";
    const action = document.createElement("select");
    action.innerHTML = '<option value="allow">Allow</option><option value="deny">Deny</option>';
    action.value = rule.action;
    action.setAttribute("aria-label", "Rule action");
    const network = document.createElement("input");
    network.value = rule.network;
    network.setAttribute("aria-label", "Network address, CIDR, or IPv4 range");
    network.autocapitalize = "none";
    network.spellcheck = false;
    const comment = document.createElement("input");
    comment.value = typeof rule.comment === "string" ? rule.comment : "";
    comment.placeholder = rule.temporary ? "Automatic emergency rule" : "Comment (optional)";
    comment.setAttribute("aria-label", "Rule comment");
    comment.maxLength = 160;
    comment.autocomplete = "off";
    const save = makeButton("Save", "secondary compact", async () => {
      try {
        await api("/api/access/rule", {
          method: "POST",
          body: {
            id: rule.id,
            action: action.value,
            network: network.value,
            comment: comment.value,
          },
        });
        toast("Access rule saved.");
        loadAccess();
      } catch (error) {
        toast(error.message, true);
      }
    });
    const remove = makeButton("Delete", "danger compact", async () => {
      if (!confirm(`Delete ${rule.action} ${rule.network}?`)) return;
      try {
        await api("/api/access/rule/delete", { method: "POST", body: { id: rule.id } });
        toast("Access rule deleted.");
        loadAccess();
      } catch (error) {
        toast(error.message, true);
      }
    });
    if (rule.temporary) {
      action.disabled = true;
      network.disabled = true;
      comment.disabled = true;
      save.disabled = true;
      const expiry = rule.expires_at ? new Date(rule.expires_at).toLocaleString() : "soon";
      save.textContent = `Emergency · expires ${expiry}`;
      save.className = "ghost compact temporary-label";
    }
    row.append(action, network, comment, save, remove);
    list.append(row);
  }
}

function bindEvents() {
  $("#theme-toggle").addEventListener("click", () => {
    setTheme(document.documentElement.dataset.theme === "dark" ? "light" : "dark");
  });
  $$(".tab").forEach((tab) => tab.addEventListener("click", () => {
    $$(".tab").forEach((item) => item.classList.toggle("active", item === tab));
    $$(".panel").forEach((panel) => panel.classList.toggle("active", panel.id === `panel-${tab.dataset.panel}`));
    if (tab.dataset.panel === "runtime") {
      loadRuntimeTargets({ selectDefault: !app.runtimeSelectedID }).then(() => {
        if (app.runtimeSelectedID) loadRuntimeTarget({ silent: true });
      });
    }
    if (tab.dataset.panel === "players") {
      loadPlayers({ silent: true });
    }
    if (tab.dataset.panel === "custompaks") {
      loadCustomPaks({ silent: true });
    }
  }));
  $$("[data-server-action]").forEach((button) =>
    button.addEventListener("click", () => serverAction(button.dataset.serverAction)));
  $("#server-prompt-form").addEventListener("submit", submitServerPrompt);
  $("#server-prompt-mode").addEventListener("change", updateServerPromptMode);
  $("#runtime-refresh-targets").addEventListener("click", async () => {
    await loadRuntimeTargets({ selectDefault: !app.runtimeSelectedID });
    if (app.runtimeSelectedID) await loadRuntimeTarget();
  });
  $("#runtime-refresh-properties").addEventListener("click", () =>
    loadRuntimeTarget({ manual: true }));
  $("#runtime-property-search").addEventListener("input", renderRuntimeProperties);
  $("#runtime-class-filter").addEventListener("change", renderRuntimeProperties);
  $("#runtime-editable-only").addEventListener("change", renderRuntimeProperties);
  $("#runtime-edit-form").addEventListener("submit", submitRuntimeEdit);
  $("#runtime-edit-close").addEventListener("click", closeRuntimeEditor);
  $("#runtime-edit-cancel").addEventListener("click", closeRuntimeEditor);
  for (const control of [
    $("#runtime-edit-select"),
    $("#runtime-edit-input"),
    $("#runtime-edit-value"),
  ]) {
    control.addEventListener("input", validateRuntimeEditor);
    control.addEventListener("change", validateRuntimeEditor);
  }
  $("#runtime-edit-dialog").addEventListener("cancel", () => {
    app.runtimeEditing = null;
  });
  $("#player-search").addEventListener("input", renderPlayerList);
  $("#player-refresh").addEventListener("click", () => loadPlayers());
  $("#player-mute-toggle").addEventListener("change", (event) =>
    setPlayerRestriction("mute", event.target.checked));
  $("#player-ban-toggle").addEventListener("change", (event) =>
    setPlayerRestriction("ban", event.target.checked));
  $("#player-comment-form").addEventListener("submit", submitPlayerComment);

  $("#language-select").addEventListener("change", async (event) => {
    try {
      await api("/api/language", { method: "POST", body: { language: event.target.value } });
      toast("Launch language saved.");
    } catch (error) {
      toast(error.message, true);
    }
  });
  $("#logout-button").addEventListener("click", async () => {
    try { await api("/logout", { method: "POST" }); } finally { location.href = "/login"; }
  });
  $("#config-file").addEventListener("change", loadConfig);
  $("#discard-config").addEventListener("click", async () => {
    if (!confirm("Discard all staged Game.ini and Engine.ini changes?")) return;
    try {
      await api("/api/config/discard", { method: "POST" });
      toast("Staged configuration discarded.");
      loadConfig();
    } catch (error) {
      toast(error.message, true);
    }
  });
  $("#add-section-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const input = $("#new-section-name");
    await mutateConfig({ action: "add_section", section: input.value });
    input.value = "";
  });
  $("#new-account-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      await api("/api/accounts/create", {
        method: "POST",
        body: { username: $("#new-account-name").value, password: $("#new-account-password").value },
      });
      event.target.reset();
      toast("Account created.");
      loadAccounts();
    } catch (error) {
      toast(error.message, true);
    }
  });
  $("#base-policy").addEventListener("change", async (event) => {
    const policy = event.target.value;
    if (policy === "all_deny" &&
        !confirm("Switch to deny all? Your current IP will receive a temporary 30-minute emergency allow rule.")) {
      loadAccess();
      return;
    }
    try {
      await api("/api/access/base", { method: "POST", body: { policy } });
      toast("Base access policy saved.");
      loadAccess();
    } catch (error) {
      toast(error.message, true);
      loadAccess();
    }
  });
  $("#new-rule-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      await api("/api/access/rule", {
        method: "POST",
        body: {
          id: "",
          action: $("#new-rule-action").value,
          network: $("#new-rule-network").value,
          comment: $("#new-rule-comment").value,
        },
      });
      $("#new-rule-network").value = "";
      $("#new-rule-comment").value = "";
      toast("Access rule added.");
      loadAccess();
    } catch (error) {
      toast(error.message, true);
    }
  });
  $("#mordhau-service-mode").addEventListener("change", (event) =>
    setServiceMode("mordhau", event.target.value));
  $("#web-service-mode").addEventListener("change", (event) =>
    setServiceMode("web", event.target.value));
  $("#web-port-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      await api("/api/services/web-port", {
        method: "POST",
        body: { port: Number($("#web-service-port").value) },
      });
      toast("Web service boot port saved.");
      loadServices();
    } catch (error) {
      toast(error.message, true);
    }
  });
  $("#start-map-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      const settings = await api("/api/services/start-map", {
        method: "POST",
        body: { start_map: $("#start-map").value },
      });
      $("#start-map").value = settings.start_map || "";
      toast(settings.start_map
        ? `Initial map saved: ${settings.start_map}`
        : "Initial map cleared; the server default will be used.");
    } catch (error) {
      toast(error.message, true);
    }
  });
  $("#server-ports-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      await api("/api/services/server-ports", {
        method: "POST",
        body: {
          game: Number($("#game-port").value),
          rcon: Number($("#rcon-port").value),
          beacon: Number($("#beacon-port").value),
          query: Number($("#query-port").value),
        },
      });
      toast("Dedicated server ports saved. Restart the server to apply them.");
      loadServices();
    } catch (error) {
      toast(error.message, true);
    }
  });
  $("#modio-settings-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      const settings = await api("/api/modio/settings", {
        method: "POST",
        body: {
          api_key: $("#modio-api-key").value,
          api_base: $("#modio-api-base").value,
        },
      });
      $("#modio-api-key").value = "";
      renderModIOSettings(settings);
      toast("mod.io API key validated and saved.");
      await loadMods();
    } catch (error) {
      $("#modio-api-key").value = "";
      toast(error.message, true);
    }
  });
  $("#modio-clear").addEventListener("click", async () => {
    if (!confirm("Clear the saved mod.io API key? Existing Mods= entries are not changed.")) return;
    try {
      await api("/api/modio/settings/clear", { method: "POST" });
      app.modPlan = null;
      app.modPlanReference = "";
      $("#mod-plan").classList.add("hidden");
      $("#mod-plan-add").classList.add("hidden");
      toast("Saved mod.io API key cleared.");
      await loadMods();
    } catch (error) {
      toast(error.message, true);
    }
  });
  $("#mod-plan-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const reference = $("#mod-reference").value.trim();
    try {
      await inspectMod(reference);
    } catch (error) {
      app.modPlan = null;
      app.modPlanReference = "";
      $("#mod-plan").classList.add("hidden");
      $("#mod-plan-add").classList.add("hidden");
      toast(error.message, true);
    }
  });
  $("#mod-plan-add").addEventListener("click", async () => {
    if (!app.modPlan || !app.modPlanReference) return;
    const count = 1 + app.modPlan.dependencies.length;
    if (!confirm(`Add ${count} Mods= entr${count === 1 ? "y" : "ies"} to Game.ini?`)) return;
    try {
      const result = await api("/api/mods/add", {
        method: "POST",
        body: { reference: app.modPlanReference },
      });
      const changed = result.added.length + result.reenabled.length;
      toast(changed
        ? `${changed} mod entr${changed === 1 ? "y" : "ies"} configured${result.staged ? " in staged Game.ini" : ""}.`
        : "Every mod in this plan is already enabled.");
      app.modPlan = null;
      app.modPlanReference = "";
      $("#mod-plan").classList.add("hidden");
      $("#mod-plan-add").classList.add("hidden");
      await Promise.all([loadMods(), loadConfig()]);
    } catch (error) {
      toast(error.message, true);
    }
  });
  $("#mods-refresh").addEventListener("click", async () => {
    const button = $("#mods-refresh");
    button.disabled = true;
    try {
      const data = await api("/api/mods/refresh", { method: "POST" });
      renderConfiguredMods(data);
      if (data.refresh && data.refresh.last_error) {
        toast(`Mod metadata refresh completed with errors: ${data.refresh.last_error}`, true);
      } else {
        toast("Mod metadata refreshed.");
      }
    } catch (error) {
      toast(error.message, true);
    } finally {
      button.disabled = false;
    }
  });
  $("#mods-refresh-minutes").addEventListener("change", async (event) => {
    const minutes = validModRefreshMinutes(event.target.value);
    if (minutes === null) {
      await loadMods();
      toast(`Enter a whole number from ${minimumModRefreshMinutes} to ${maximumModRefreshMinutes} minutes.`, true);
      return;
    }
    try {
      const data = await api("/api/mods/refresh/settings", {
        method: "POST",
        body: { minutes },
      });
      renderConfiguredMods(data);
      toast(`Server-wide mod refresh interval set to ${minutes} minute${minutes === 1 ? "" : "s"}.`);
    } catch (error) {
      await loadMods();
      toast(error.message, true);
    }
  });
  $("#mods-restart-on-update").addEventListener("change", async (event) => {
    const enabled = event.target.checked;
    event.target.disabled = true;
    try {
      const data = await api("/api/mods/refresh/settings", {
        method: "POST",
        body: { restart_on_update: enabled },
      });
      renderConfiguredMods(data);
      toast(enabled
        ? "Automatic server restart on active mod update enabled."
        : "Automatic server restart on mod update disabled.");
    } catch (error) {
      await loadMods();
      toast(error.message, true);
    }
  });
  $("#custompak-refresh").addEventListener("click", () => loadCustomPaks());
  $("#custompak-browse").addEventListener("click", () => {
    $("#custompak-file").click();
  });
  $("#custompak-file").addEventListener("change", (event) => {
    uploadCustomPak(event.target.files && event.target.files[0]);
  });
  $("#custompak-upload-cancel").addEventListener("click", () => {
    if (app.customPakUploadXHR) app.customPakUploadXHR.abort();
  });
  const customPakDropzone = $("#custompak-dropzone");
  for (const eventName of ["dragenter", "dragover"]) {
    customPakDropzone.addEventListener(eventName, (event) => {
      event.preventDefault();
      if (!app.customPakUploadXHR) customPakDropzone.classList.add("dragging");
    });
  }
  customPakDropzone.addEventListener("dragleave", (event) => {
    if (!customPakDropzone.contains(event.relatedTarget)) {
      customPakDropzone.classList.remove("dragging");
    }
  });
  customPakDropzone.addEventListener("drop", (event) => {
    event.preventDefault();
    customPakDropzone.classList.remove("dragging");
    const files = event.dataTransfer && event.dataTransfer.files;
    if (!files || files.length !== 1) {
      toast("Drop exactly one PAK file.", true);
      return;
    }
    uploadCustomPak(files[0]);
  });
  window.addEventListener("dragover", (event) => {
    if (event.dataTransfer && [...event.dataTransfer.types].includes("Files")) {
      event.preventDefault();
    }
  });
  window.addEventListener("drop", (event) => {
    if (!customPakDropzone.contains(event.target)) event.preventDefault();
  });
}

async function initialize() {
  initializeTheme();
  clearLegacyModRefreshPreference();
  bindEvents();
  updateServerPromptMode();
  try {
    const me = await api("/api/me");
    app.csrf = me.csrf;
    app.username = me.username;
    $("#current-user").textContent = `${me.username} · ${me.current_ip}`;
    const [snapshot, eventHistory] = await Promise.all([
      api("/api/snapshot"),
      api("/api/server/events/history"),
    ]);
    appendServerEvents(eventHistory.events || []);
    renderSnapshot(snapshot);
    await Promise.all([
      loadConfig(),
      loadMods(),
      loadCustomPaks({ silent: true }),
      loadAccounts(),
      loadAccess(),
      loadServices(),
      loadRuntimeTargets({ silent: true }),
    ]);
    const stream = new EventSource("/api/events");
    stream.addEventListener("snapshot", (event) => {
      try { renderSnapshot(JSON.parse(event.data)); } catch (_) {}
    });
    stream.onerror = () => {
      const status = $("#event-source-status");
      status.textContent = "Live stream reconnecting";
      status.classList.remove("connected");
    };
    setInterval(refreshRuntimeLive, 2000);
    setInterval(renderPlayerLiveDuration, 1000);
    setInterval(() => {
      if (playersPanelActive()) loadPlayers({ silent: true });
    }, 30000);
  } catch (error) {
    toast(error.message, true);
  }
}

initialize();
