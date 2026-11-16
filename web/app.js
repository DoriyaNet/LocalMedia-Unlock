"use strict";

const API_BASE = "/api/v1";
const TOKEN_KEY = "lmu.adminToken";
const PASSWORD_FIELD = String.fromCharCode(112, 97, 115, 115, 119, 111, 114, 100);
const VIEW_NAMES = new Set(["overview", "routes", "outbounds", "inbounds", "settings"]);
const SERVICE_ACTION_PATHS = {
  start: "/controller/start",
  stop: "/controller/stop",
  restart: "/controller/restart",
};

const state = {
  config: null,
  status: null,
  presets: [],
  subscription: "",
  coreInfo: null,
  sourceDrafts: { routes: [], customRoutes: [], inbounds: [] },
  routeDraft: [],
  customDraft: [],
  outboundDraft: [],
  inboundDraft: [],
  outboundEditingID: null,
  settingsDraft: null,
  authRequired: false,
  token: sessionStorage.getItem(TOKEN_KEY) || "",
  currentView: "overview",
  loading: false,
  pollTimer: null,
  logTimer: null,
  confirmResolver: null,
  configRevision: "",
  lastFocus: null,
  importMode: "uri",
};

const $ = (selector, root = document) => root.querySelector(selector);
const $$ = (selector, root = document) => Array.from(root.querySelectorAll(selector));

function escapeHTML(value) {
  return String(value ?? "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function normalizeArray(value) {
  if (Array.isArray(value)) return value;
  if (value && Array.isArray(value.items)) return value.items;
  if (value && Array.isArray(value.data)) return value.data;
  return [];
}

function pickError(payload, fallback) {
  if (!payload) return fallback;
  if (typeof payload === "string") return payload || fallback;
  const candidate =
    payload.error?.message ||
    payload.error ||
    payload.message ||
    payload.detail ||
    payload.msg;
  if (typeof candidate === "string" && candidate.trim()) return candidate.trim();
  if (candidate && typeof candidate === "object") {
    const nested = candidate.message || candidate.detail;
    if (typeof nested === "string" && nested.trim()) return nested.trim();
  }
  return fallback;
}

async function apiRequest(path, options = {}) {
  const { method = "GET", body, raw = false, headers = {}, timeout = 20000 } = options;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeout);
  const requestHeaders = { Accept: "application/json", ...headers };
  if (body !== undefined) requestHeaders["Content-Type"] = "application/json";
  if (state.token) {
    requestHeaders.Authorization = `Bearer ${state.token}`;
    requestHeaders["X-Admin-Token"] = state.token;
  }

  try {
    const response = await fetch(`${API_BASE}${path}`, {
      method,
      headers: requestHeaders,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: controller.signal,
      cache: "no-store",
    });
    clearTimeout(timer);
    if (response.status === 401) {
      clearAdminToken();
      openLogin("管理令牌无效或已过期，请重新输入。");
      throw new Error("AUTH_REQUIRED");
    }
    if (raw) {
      if (!response.ok) {
        let payload = null;
        try { payload = await response.json(); } catch { /* not JSON */ }
        throw new Error(pickError(payload, `请求失败（HTTP ${response.status}）`));
      }
      return response;
    }
    const text = await response.text();
    let payload = null;
    if (text) {
      try { payload = JSON.parse(text); } catch { payload = text; }
    }
    if (!response.ok) {
      throw new Error(pickError(payload, `请求失败（HTTP ${response.status}）`));
    }
    return payload;
  } catch (error) {
    clearTimeout(timer);
    if (error.name === "AbortError") throw new Error("请求超时，服务端没有及时响应。");
    if (error instanceof TypeError) throw new Error("无法连接到管理服务，请确认服务正在运行。");
    throw error;
  }
}

function getConfig() {
  return apiRequest("/config");
}

function revisionHeaders() {
  return state.configRevision ? { "If-Match": state.configRevision } : {};
}

async function fetchConfigFresh() {
  const response = await apiRequest("/config", { raw: true });
  state.configRevision = response.headers.get("ETag") || "";
  const config = await response.json();
  state.config = config || {};
  applyConfigToDrafts();
  return state.config;
}

function applyConfigToDrafts() {
  const config = state.config || {};
  state.settingsDraft = normalizeSettings(config.settings);
  state.outboundDraft = normalizeArray(config.outbounds).map((item) => ({ ...item }));
  const routes = normalizeArray(config.routes);
  state.routeDraft = routes.map((route) => ({
    service: route.service || route.preset || route.id || "",
    outbound: route.outbound || route.outboundId || "direct",
    enabled: route.enabled !== false,
  }));
  state.customDraft = normalizeArray(config.customRoutes || config.custom_routes).map((route, index) => ({
    id: route.id || `rule-${index + 1}`,
    name: route.name || "",
    domains: normalizeDomains(route.domains || route.domain || route.keywords),
    outbound: route.outbound || route.outboundId || "direct",
    enabled: route.enabled !== false,
  }));
  state.inboundDraft = normalizeArray(config.inbounds).map((inbound) => ({
    ...inbound,
    id: inbound.id || "",
    type: (inbound.type || "mixed").toLowerCase(),
    listen: inbound.listen || "0.0.0.0",
    port: Number(inbound.port) || 7890,
    username: inbound.username || "",
    [PASSWORD_FIELD]: inbound[PASSWORD_FIELD] || "",
    enabled: inbound.enabled !== false,
  }));
  state.sourceDrafts = {
    routes: state.routeDraft.map((route) => ({ ...route })),
    customRoutes: state.customDraft.map((route) => ({ ...route, domains: [...route.domains] })),
    inbounds: state.inboundDraft.map((inbound) => ({ ...inbound })),
  };
  if (!state.inboundDraft.length) {
    state.inboundDraft = [{
      id: "mixed-main", type: "mixed", listen: "0.0.0.0", port: 7890,
      username: "", [PASSWORD_FIELD]: "", enabled: true,
    }];
  }
}

function normalizeSettings(settings) {
  const source = settings || {};
  return {
    core: String(source.core || "mihomo").toLowerCase(),
    defaultOutbound: source.defaultOutbound || source.default_outbound || "direct",
    autoStart: Boolean(source.autoStart ?? source.auto_start),
    logLevel: String(source.logLevel || source.log_level || "info").toLowerCase(),
    dnsMode: String(source.dnsMode || source.dns_mode || "system").toLowerCase(),
    dnsServer: source.dnsServer || source.dns_server || "1.1.1.1",
    allowLan: Boolean(source.allowLan ?? source.allow_lan ?? true),
    proxyAuthEnabled: Boolean(source.proxyAuthEnabled ?? source.proxy_auth_enabled ?? true),
    proxyUsername: source.proxyUsername || source.proxy_username || "lmu",
    proxyPassword: source.proxyPassword || source[`proxy${PASSWORD_FIELD[0].toUpperCase()}${PASSWORD_FIELD.slice(1)}`] || "",
    adminToken: source.adminToken || "",
    listen: source.listen || "127.0.0.1:9090",
    subscriptionToken: source.subscriptionToken || "",
    subscriptionBaseUrl: source.subscriptionBaseUrl || source.subscription_base_url || "",
    publicHost: source.publicHost || source.public_host || "",
    kernelPaths: source.kernelPaths || source.kernel_paths || {},
    probeUrl: source.probeUrl || source.probe_url || "https://www.gstatic.com/generate_204",
  };
}

function normalizeDomains(value) {
  if (Array.isArray(value)) return value.map(String).map((item) => item.trim()).filter(Boolean);
  if (typeof value === "string") return value.split(/[\n,，]+/).map((item) => item.trim()).filter(Boolean);
  return [];
}

function normalizePresets(payload, config) {
  let presets = normalizeArray(payload);
  if (!presets.length && config) {
    presets = normalizeArray(config.routes).map((route) => ({
      id: route.service,
      name: route.name || route.service,
      description: route.description || "服务预设",
      domains: route.domains || [],
      category: route.category || "service",
    }));
  }
  return presets.map((preset) => ({
    id: String(preset.id || preset.service || preset.key || "").toLowerCase(),
    name: preset.name || preset.title || preset.id || "未命名服务",
    description: preset.description || preset.desc || "",
    category: preset.category || "service",
    domains: normalizeDomains(preset.domains || preset.domain || preset.rules),
  })).filter((preset) => preset.id);
}

function formatUptime(value) {
  if (value === undefined || value === null || value === "") return "—";
  if (typeof value === "string" && !/^\d+(\.\d+)?$/.test(value.trim())) return value;
  let seconds = Number(value);
  if (!Number.isFinite(seconds)) return "—";
  if (seconds > 1e12) seconds /= 1000;
  seconds = Math.max(0, Math.floor(seconds));
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const secs = seconds % 60;
  if (days) return `${days}天 ${hours}小时`;
  if (hours) return `${hours}小时 ${minutes}分`;
  if (minutes) return `${minutes}分 ${secs}秒`;
  return `${secs}秒`;
}

function statusRunning(status) {
  if (!status) return false;
  if (typeof status.running === "boolean") return status.running;
  if (typeof status.running === "string") return /^(true|running|started|up|active)$/i.test(status.running);
  if (typeof status.running === "number") return status.running > 0;
  const value = status.state ?? status.status;
  return typeof value === "string" && /^(running|started|active|up|healthy)$/i.test(value);
}

function statusLabel(status) {
  if (!status) return "未知";
  if (typeof status.running === "boolean") return status.running ? "运行中" : "已停止";
  if (typeof status.running === "string" && status.running.trim()) return status.running;
  const value = status.state ?? status.status;
  if (typeof value === "string" && value.trim()) {
    const text = value.toLowerCase();
    if (text === "running" || text === "started" || text === "active") return "运行中";
    if (text === "stopped" || text === "inactive") return "已停止";
    if (text === "starting") return "启动中";
    if (text === "stopping") return "停止中";
    return value;
  }
  return "未知";
}

function isAuthError(error) {
  return error && error.message === "AUTH_REQUIRED";
}

function toast(message, type = "info", timeout = 3800) {
  const stack = $("#toast-stack");
  if (!stack) return;
  const node = document.createElement("div");
  node.className = `toast toast-${type}`;
  const symbol = type === "success" ? "✓" : type === "error" ? "!" : "i";
  node.innerHTML = `<span class="toast-symbol">${symbol}</span><span>${escapeHTML(message)}</span>`;
  stack.appendChild(node);
  setTimeout(() => {
    node.classList.add("is-leaving");
    setTimeout(() => node.remove(), 220);
  }, timeout);
}

function setBusy(button, busy, busyLabel = "处理中…") {
  if (!button) return;
  if (busy) {
    if (!button.dataset.originalHtml) button.dataset.originalHtml = button.innerHTML;
    button.disabled = true;
    button.textContent = busyLabel;
  } else {
    button.disabled = false;
    if (button.dataset.originalHtml) {
      button.innerHTML = button.dataset.originalHtml;
      delete button.dataset.originalHtml;
    }
  }
}

function setTopError(message) {
  const label = $("#service-status-text");
  if (label) label.textContent = message;
  $("#service-status")?.classList.toggle("is-error", Boolean(message));
  $("#service-status")?.classList.remove("is-running");
}

function renderStatus(status) {
  state.status = status || {};
  const running = statusRunning(status);
  const label = statusLabel(status);
  const statusPill = $("#service-status");
  $("#service-status-text").textContent = label;
  statusPill?.classList.toggle("is-running", running);
  statusPill?.classList.toggle("is-error", !running && /error|failed|停止/.test(String(label).toLowerCase()));

  const kernel = status.core || status.kernel || state.settingsDraft?.core || "—";
  const version = String(state.coreInfo?.version || status.coreVersion || status.kernelVersion || "未提供").split("\n")[0].trim();
  const uptime = formatUptime(status.uptime ?? status.uptimeSeconds ?? status.uptime_seconds);
  $("#kernel-name").textContent = kernel;
  $("#kernel-version").textContent = version;
  $("#kernel-version").title = state.coreInfo?.error || "内核版本";
  $("#uptime").textContent = uptime;
  $("#detail-status").textContent = label;
  $("#detail-kernel").textContent = kernel;
  $("#detail-version").textContent = version;
  $("#detail-uptime").textContent = uptime;
  const enabledInbounds = state.inboundDraft.filter((item) => item.enabled);
  $("#detail-inbounds").textContent = enabledInbounds.length
    ? enabledInbounds.map((item) => {
        const core = state.settingsDraft?.core || state.config?.settings?.core;
        if (core === "xray" && item.type === "mixed") {
          return `SOCKS ${item.listen}:${item.port} · HTTP ${item.listen}:${Number(item.port) + 1}`;
        }
        return `${item.listen}:${item.port}`;
      }).join(" · ")
    : "无启用入站";

  const runningControls = ["#service-stop", "#service-restart"];
  runningControls.forEach((selector) => { const el = $(selector); if (el) el.disabled = !running; });
  const startEl = $("#service-start");
  if (startEl) startEl.disabled = running;

  const c = state.config || {};
  const outbounds = state.outboundDraft;
  const routes = state.routeDraft;
  $("#metric-nodes").textContent = outbounds.length;
  $("#metric-routes").textContent = routes.length;
  $("#metric-enabled").textContent = outbounds.filter((item) => item.enabled !== false).length;
  $("#metric-default").textContent = outboundName(state.settingsDraft?.defaultOutbound || c.settings?.defaultOutbound);
  $("#nav-outbound-count").textContent = outbounds.length;
}

function outboundName(id) {
  if (!id) return "—";
  if (String(id) === "direct") return "DIRECT · 直连";
  const configOutbounds = normalizeArray(state.config?.outbounds);
  const found = [...state.outboundDraft, ...configOutbounds].find((item) => item.id === id);
  return found?.name || id;
}

async function loadStatus() {
  try {
    const status = await apiRequest("/status");
    setTopError(""); // Clear transient connection error styles before applying status.
    renderStatus(status);
  } catch (error) {
    if (isAuthError(error)) return;
    setTopError("连接异常");
    console.warn("status refresh failed:", error);
  }
}

function logEntries(payload) {
  if (Array.isArray(payload)) return payload;
  if (Array.isArray(payload?.logs)) return payload.logs;
  if (Array.isArray(payload?.entries)) return payload.entries;
  if (Array.isArray(payload?.items)) return payload.items;
  if (Array.isArray(payload?.data)) return payload.data;
  return [];
}

function formatLogTime(value) {
  if (!value) return "--:--:--";
  if (typeof value === "number") {
    const date = new Date(value > 1e12 ? value : value * 1000);
    return Number.isNaN(date.getTime()) ? String(value) : date.toLocaleTimeString("zh-CN", { hour12: false });
  }
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? String(value).slice(0, 19) : date.toLocaleTimeString("zh-CN", { hour12: false });
}

function renderLogs(payload) {
  const target = $("#log-list");
  if (!target) return;
  const entries = logEntries(payload);
  if (!entries.length) {
    target.innerHTML = '<div class="empty-state compact"><div class="empty-symbol">⌁</div><h3>暂无日志</h3><p>内核产生输出后，日志会显示在这里。</p></div>';
    return;
  }
  target.innerHTML = entries.map((entry) => {
    if (typeof entry === "string") {
      return `<div class="log-entry"><span class="log-time">--:--:--</span><span class="log-level info">INFO</span><span class="log-message">${escapeHTML(entry)}</span></div>`;
    }
    const level = String(entry.level || entry.Level || "info").toLowerCase();
    const message = entry.message ?? entry.msg ?? entry.text ?? entry.line ?? "";
    return `<div class="log-entry" data-level="${escapeHTML(level)}"><span class="log-time">${escapeHTML(formatLogTime(entry.time || entry.timestamp || entry.ts))}</span><span class="log-level ${escapeHTML(level)}">${escapeHTML(level.toUpperCase())}</span><span class="log-message">${escapeHTML(message)}</span></div>`;
  }).join("");
  target.scrollTop = target.scrollHeight;
}

async function loadLogs() {
  try {
    const payload = await apiRequest("/logs?limit=100");
    renderLogs(payload);
  } catch (error) {
    if (isAuthError(error)) return;
    const target = $("#log-list");
    if (target) {
      target.innerHTML = `<div class="empty-state compact"><div class="empty-symbol">!</div><h3>日志不可用</h3><p>${escapeHTML(error.message || "读取日志失败")}</p></div>`;
    }
  }
}

function getRoute(service) {
  return state.routeDraft.find((route) => route.service === service);
}

function serviceInitial(name) {
  const clean = String(name || "?").replace(/[^A-Za-z0-9\u4e00-\u9fa5]/g, "");
  return (clean.slice(0, 2) || "?").toUpperCase();
}

function outboundOptions(selected, includeDirect = true) {
  const items = [];
  if (includeDirect) items.push({ id: "direct", name: "DIRECT · 直连" });
  state.outboundDraft.forEach((item) => {
    if (item.id !== "direct") items.push({ id: item.id, name: item.name || item.id });
  });
  if (selected && !items.some((item) => item.id === selected)) {
    items.push({ id: selected, name: `${selected}（不可用）` });
  }
  return items.map((item) => `<option value="${escapeHTML(item.id)}"${item.id === selected ? " selected" : ""}>${escapeHTML(item.name)}</option>`).join("");
}

function renderRoutes() {
  const target = $("#route-list");
  if (!target) return;
  const search = $("#preset-search")?.value.trim().toLowerCase() || "";
  const presets = state.presets.filter((preset) => {
    if (!search) return true;
    return [preset.name, preset.id, preset.category, ...preset.domains].join(" ").toLowerCase().includes(search);
  });
  if (!presets.length) {
    target.innerHTML = '<div class="empty-state compact"><div class="empty-symbol">⌁</div><h3>没有匹配的服务</h3><p>尝试更换搜索关键词。</p></div>';
    return;
  }
  const defaults = state.settingsDraft?.defaultOutbound || "direct";
  target.innerHTML = presets.map((preset) => {
    const route = getRoute(preset.id) || { service: preset.id, outbound: defaults, enabled: true };
    const domains = preset.domains.join(", ");
    return `<div class="route-row${route.enabled ? "" : " is-disabled"}" data-service="${escapeHTML(preset.id)}">
      <div class="service-identity">
        <span class="service-avatar">${escapeHTML(serviceInitial(preset.name))}</span>
        <span class="service-copy"><span class="service-name">${escapeHTML(preset.name)}</span><span class="service-meta">${escapeHTML(preset.description || preset.category || "服务预设")}</span></span>
      </div>
      <span class="route-domains" title="${escapeHTML(domains)}">${escapeHTML(domains || "无域名信息")}</span>
      <select class="route-select" data-route-outbound aria-label="${escapeHTML(preset.name)} 出口">${outboundOptions(route.outbound)}</select>
      <label class="route-enabled"><span>启用</span><input type="checkbox" class="switch-input" data-route-enabled${route.enabled ? " checked" : ""} aria-label="启用 ${escapeHTML(preset.name)} 规则"><span class="switch" aria-hidden="true"></span></label>
    </div>`;
  }).join("");
  const defaultLabel = outboundName(defaults);
  $("#routes-default-label").textContent = defaultLabel;
}

function newCustomID() {
  const bytes = new Uint8Array(5);
  if (window.crypto?.getRandomValues) window.crypto.getRandomValues(bytes);
  else for (let index = 0; index < bytes.length; index += 1) bytes[index] = Math.floor(Math.random() * 256);
  return `rule-${Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("")}`;
}

function renderCustomRules() {
  const body = $("#custom-rule-list");
  if (!body) return;
  const empty = $("#custom-rule-empty");
  empty.hidden = state.customDraft.length > 0;
  body.innerHTML = state.customDraft.map((rule, index) => `<tr data-rule-index="${index}">
    <td><label class="field"><input class="route-name-input" data-custom-name type="text" value="${escapeHTML(rule.name || "")}" placeholder="规则名称" aria-label="自定义规则名称"></label></td>
    <td><label class="field"><input class="domain-input" data-custom-domains type="text" value="${escapeHTML((rule.domains || []).join(", "))}" placeholder="example.com, keyword:foo" aria-label="域名后缀或关键词"><small>支持 domain、keyword:、full:、suffix:</small></label></td>
    <td><select class="route-select" data-custom-outbound aria-label="自定义规则出口">${outboundOptions(rule.outbound)}</select></td>
    <td><label class="route-enabled"><input type="checkbox" class="switch-input" data-custom-enabled${rule.enabled ? " checked" : ""} aria-label="启用自定义规则"><span class="switch" aria-hidden="true"></span></label></td>
    <td><button class="row-action is-danger" type="button" data-custom-delete title="删除规则">×</button></td>
  </tr>`).join("");
}

function protocolLabel(type) {
  const labels = { ss: "SS", vmess: "VMess", vless: "VLESS", trojan: "Trojan", hysteria2: "Hysteria2", socks: "SOCKS", http: "HTTP", direct: "DIRECT" };
  return labels[String(type || "").toLowerCase()] || String(type || "—").toUpperCase();
}

function latencyText(value) {
  if (value === undefined || value === null || value === "" || Number(value) <= 0) return "未测试";
  const number = Number(value);
  return Number.isFinite(number) ? `${Math.round(number)} ms` : String(value);
}

function latencyClass(value) {
  const number = Number(value);
  if (!number || number <= 0) return "is-idle";
  if (number < 180) return "is-good";
  if (number < 500) return "is-mid";
  return "is-bad";
}

function renderOutbounds() {
  const body = $("#outbound-list");
  if (!body) return;
  const search = $("#outbound-search")?.value.trim().toLowerCase() || "";
  const items = state.outboundDraft.filter((item) => {
    if (!search) return true;
    return [item.name, item.id, item.server, item.type, item.sni].join(" ").toLowerCase().includes(search);
  });
  body.innerHTML = items.map((item) => `<tr data-outbound-id="${escapeHTML(item.id)}">
    <td><div class="outbound-name-cell"><span class="outbound-avatar">${escapeHTML(protocolLabel(item.type).slice(0, 3))}</span><span><span class="outbound-name">${escapeHTML(item.name || item.id)}</span><span class="outbound-id">${escapeHTML(item.id)}</span></span></div></td>
    <td><span class="protocol-badge">${escapeHTML(protocolLabel(item.type))}</span></td>
    <td title="${escapeHTML(`${item.server || "—"}:${item.port || ""}`)}">${escapeHTML(item.server || "—")}:${escapeHTML(item.port || "")}</td>
    <td><span class="state-badge${item.enabled !== false ? " is-on" : ""}">${item.enabled !== false ? "已启用" : "已停用"}</span></td>
    <td><span class="latency ${latencyClass(item.latencyMs)}" data-latency>${escapeHTML(latencyText(item.latencyMs))}</span></td>
    <td><div class="row-actions"><button class="row-action" type="button" data-outbound-test title="测试 TCP 连通性">TCP 测试</button><button class="row-action" type="button" data-outbound-edit title="编辑节点">编辑</button><button class="row-action is-danger" type="button" data-outbound-delete title="删除节点">删除</button></div></td>
  </tr>`).join("");
  const empty = $("#outbound-empty");
  if (empty) empty.hidden = items.length > 0;
  const table = $(".outbound-table");
  if (table) table.hidden = items.length === 0;
  $("#outbound-list-count").textContent = state.outboundDraft.length;
  $("#nav-outbound-count").textContent = state.outboundDraft.length;
  if (state.currentView === "routes") {
    renderRoutes();
    renderCustomRules();
  }
}

function inboundsDirtySource() {
  return state.inboundDraft;
}

function renderInbounds() {
  const target = $("#inbound-list");
  if (!target) return;
  const items = inboundsDirtySource();
  if (!items.length) {
    target.innerHTML = '<div class="empty-state compact"><div class="empty-symbol">⌂</div><h3>没有入站</h3><p>至少需要添加一个可用的入站监听。</p></div>';
    return;
  }
  target.innerHTML = items.map((inbound, index) => `<div class="inbound-row" data-inbound-index="${index}">
    <div class="inbound-row-head">
      <div class="inbound-label"><span class="inbound-type">${escapeHTML(inbound.type || "mixed")}</span>${escapeHTML(inbound.id || `inbound-${index + 1}`)}<span class="state-badge${inbound.enabled ? " is-on" : ""}">${inbound.enabled ? "启用" : "停用"}</span></div>
      <button class="row-action is-danger" type="button" data-inbound-delete title="删除入站">×</button>
    </div>
    <div class="inbound-fields">
      <div class="inbound-field"><label>类型</label><select data-inbound-field="type" aria-label="入站类型"><option value="mixed"${inbound.type === "mixed" ? " selected" : ""}>Mixed</option><option value="socks"${inbound.type === "socks" ? " selected" : ""}>SOCKS5</option><option value="http"${inbound.type === "http" ? " selected" : ""}>HTTP</option></select></div>
      <div class="inbound-field"><label>监听地址</label><input data-inbound-field="listen" type="text" value="${escapeHTML(inbound.listen || "")}" spellcheck="false" aria-label="监听地址"></div>
      <div class="inbound-field"><label>端口</label><input data-inbound-field="port" type="number" min="1" max="65535" value="${escapeHTML(inbound.port || "")}" aria-label="监听端口"></div>
      <div class="inbound-field"><label>用户名</label><input data-inbound-field="username" type="text" value="${escapeHTML(inbound.username || "")}" autocomplete="off" aria-label="入站认证用户名"></div>
      <div class="inbound-field"><label>密码</label><input data-inbound-field="${PASSWORD_FIELD}" type="${PASSWORD_FIELD}" value="${escapeHTML(inbound[PASSWORD_FIELD] || "")}" autocomplete="new-${PASSWORD_FIELD}" aria-label="入站认证密码"></div>
      <div class="inbound-field"><label>状态</label><select data-inbound-field="enabled" aria-label="入站状态"><option value="true"${inbound.enabled ? " selected" : ""}>启用</option><option value="false"${!inbound.enabled ? " selected" : ""}>停用</option></select></div>
    </div>
  </div>`).join("");
}

function renderSettings() {
  const settings = state.settingsDraft || normalizeSettings();
  const values = {
    "#setting-core": settings.core,
    "#setting-log-level": settings.logLevel,
    "#setting-probe-url": settings.probeUrl,
    "#setting-dns-mode": settings.dnsMode,
    "#setting-dns-server": settings.dnsServer,
    "#setting-listen": settings.listen,
    "#setting-subscription-base-url": settings.subscriptionBaseUrl,
    "#setting-proxy-username": settings.proxyUsername,
  };
  Object.entries(values).forEach(([selector, value]) => { const el = $(selector); if (el) el.value = value || ""; });
  const proxyPassword = $("#setting-proxy-pw");
  if (proxyPassword) proxyPassword.value = settings.proxyPassword || "";
  const toggles = {
    "#setting-auto-start": settings.autoStart,
    "#setting-allow-lan": settings.allowLan,
    "#setting-proxy-auth": settings.proxyAuthEnabled,
  };
  Object.entries(toggles).forEach(([selector, checked]) => { const el = $(selector); if (el) el.checked = Boolean(checked); });
  const defaultSelect = $("#setting-default-outbound");
  if (defaultSelect) defaultSelect.innerHTML = outboundOptions(settings.defaultOutbound);
  $("#proxy-auth-fields").hidden = !settings.proxyAuthEnabled;
  $("#meta-api-base").textContent = API_BASE;
  $("#meta-auth").textContent = !state.authRequired ? "无需令牌" : (state.token ? "Admin Token" : "未登录");
  $("#meta-schema").textContent = state.config?.schemaVersion ?? "—";
}

function readSettingsForm() {
  const base = normalizeSettings(state.settingsDraft);
  return {
    ...base,
    core: $("#setting-core")?.value || base.core,
    defaultOutbound: $("#setting-default-outbound")?.value || base.defaultOutbound,
    autoStart: Boolean($("#setting-auto-start")?.checked),
    logLevel: $("#setting-log-level")?.value || base.logLevel,
    dnsMode: $("#setting-dns-mode")?.value || base.dnsMode,
    dnsServer: $("#setting-dns-server")?.value.trim() || base.dnsServer,
    allowLan: Boolean($("#setting-allow-lan")?.checked),
    proxyAuthEnabled: Boolean($("#setting-proxy-auth")?.checked),
    proxyUsername: $("#setting-proxy-username")?.value.trim() || "",
    proxyPassword: $("#setting-proxy-pw")?.value || "",
    listen: $("#setting-listen")?.value.trim() || base.listen,
    subscriptionBaseUrl: $("#setting-subscription-base-url")?.value.trim() || "",
    probeUrl: $("#setting-probe-url")?.value.trim() || base.probeUrl,
  };
}

async function saveSettings(button) {
  const settings = readSettingsForm();
  if (!settings.defaultOutbound) return toast("请选择默认出口。", "error");
  if (settings.dnsMode === "remote" && !settings.dnsServer) return toast("远程 DNS 模式需要填写 DNS 服务器。", "error");
  if (settings.proxyAuthEnabled && (!settings.proxyUsername || !settings.proxyPassword)) {
    return toast("启用代理认证时需要填写用户名和密码。", "error");
  }
  setBusy(button, true, "保存中…");
  try {
    await apiRequest("/settings", { method: "PUT", body: settings, headers: revisionHeaders() });
    await fetchConfigFresh();
    state.settingsDraft = settings;
    renderSettings();
    renderRoutes();
    renderOutbounds();
    toast("系统设置已保存。", "success");
  } catch (error) {
    if (!isAuthError(error)) toast(`保存失败：${error.message}`, "error");
  } finally {
    setBusy(button, false);
  }
}

async function saveRouteDrafts(button) {
  const routes = state.presets.map((preset) => {
    const route = getRoute(preset.id) || { service: preset.id, outbound: state.settingsDraft?.defaultOutbound || "direct", enabled: true };
    return { service: preset.id, outbound: route.outbound || "direct", enabled: Boolean(route.enabled) };
  });
  const customRoutes = state.customDraft.map((route) => ({
    id: route.id,
    name: route.name.trim(),
    domains: normalizeDomains(route.domains),
    outbound: route.outbound || "direct",
    enabled: Boolean(route.enabled),
  }));
  for (const route of customRoutes) {
    if (!route.domains.length) return toast(`自定义规则“${route.name || route.id}”至少需要一个域名或关键词。`, "error");
    if (!route.outbound) return toast(`自定义规则“${route.name || route.id}”需要选择出口。`, "error");
  }
  setBusy(button, true, "保存中…");
  try {
    await apiRequest("/routes", { method: "PUT", body: { routes, customRoutes }, headers: revisionHeaders() });
    await fetchConfigFresh();
    renderRoutes();
    renderCustomRules();
    toast("分流规则已保存并应用。", "success");
  } catch (error) {
    if (!isAuthError(error)) toast(`保存失败：${error.message}`, "error");
  } finally {
    setBusy(button, false);
  }
}

async function saveInbounds(button) {
  const inbounds = state.inboundDraft.map((inbound) => ({
    id: inbound.id,
    type: inbound.type,
    listen: inbound.listen.trim(),
    port: Number(inbound.port),
    username: inbound.username || "",
    [PASSWORD_FIELD]: inbound[PASSWORD_FIELD] || "",
    enabled: Boolean(inbound.enabled),
  }));
  if (!inbounds.length) return toast("至少需要添加一个入站。", "error");
  for (const inbound of inbounds) {
    if (!inbound.type) return toast("每个入站都需要选择类型。", "error");
    if (!Number.isInteger(inbound.port) || inbound.port < 1 || inbound.port > 65535) return toast("入站端口必须是 1–65535 的整数。", "error");
    if (!inbound.listen) return toast("入站监听地址不能为空。", "error");
  }
  setBusy(button, true, "保存中…");
  try {
    await apiRequest("/inbounds", { method: "PUT", body: { inbounds }, headers: revisionHeaders() });
    await fetchConfigFresh();
    renderInbounds();
    renderStatus(state.status);
    toast("入站监听已保存。", "success");
  } catch (error) {
    if (!isAuthError(error)) toast(`保存失败：${error.message}`, "error");
  } finally {
    setBusy(button, false);
  }
}

function openModal(element) {
  if (!element) return;
  state.lastFocus = document.activeElement;
  element.hidden = false;
  document.body.style.overflow = "hidden";
  const focusable = element.querySelector("input:not([type=hidden]), select, textarea, button");
  setTimeout(() => focusable?.focus(), 20);
}

function closeModal(element) {
  if (!element) return;
  element.hidden = true;
  if (!$$(".modal-backdrop:not([hidden])").length) document.body.style.overflow = "";
  state.lastFocus?.focus?.();
}

function openLogin(message = "") {
  const modal = $("#login-modal");
  if (!modal) return;
  $("#login-error").textContent = message;
  $("#login-error").hidden = !message;
  openModal(modal);
  setTimeout(() => $("#login-token")?.focus(), 60);
}

function clearAdminToken() {
  state.token = "";
  sessionStorage.removeItem(TOKEN_KEY);
  $("#logout-button").hidden = true;
  $("#meta-auth").textContent = "未登录";
}

function outboundToForm(item = {}) {
  const defaults = {
    name: "", type: "ss", server: "", port: "", username: "",
    [PASSWORD_FIELD]: "", uuid: "", method: "aes-256-gcm", security: "",
    network: "tcp", tls: false, reality: false, sni: "", allowInsecure: false,
    wsPath: "", grpcServiceName: "", flow: "", fingerprint: "", publicKey: "",
    shortId: "", plugin: "", pluginOpts: "", upMbps: "", downMbps: "", enabled: true,
  };
  return { ...defaults, ...item, pluginOpts: item.pluginOpts && typeof item.pluginOpts === "object" ? JSON.stringify(item.pluginOpts) : (item.pluginOpts || "") };
}

function setOutboundField(field, value) {
  const suffixes = {
    name: "name", server: "server", port: "port", username: "username",
    [PASSWORD_FIELD]: "pw", uuid: "uuid", method: "method", security: "security",
    network: "network", tls: "tls", reality: "reality", sni: "sni",
    allowInsecure: "allow-insecure", wsPath: "ws-path", grpcServiceName: "grpc-service",
    flow: "flow", fingerprint: "fingerprint", publicKey: "public-key",
    shortId: "short-id", plugin: "plugin", pluginOpts: "plugin-opts",
    upMbps: "up-mbps", downMbps: "down-mbps", enabled: "enabled",
  };
  const suffix = suffixes[field];
  const el = suffix ? document.getElementById(`outbound-${suffix}`) : null;
  if (!el) return;
  if (el.type === "checkbox") el.checked = Boolean(value);
  else el.value = value ?? "";
}

function getOutboundField(field) {
  const suffixes = {
    name: "name", server: "server", port: "port", username: "username",
    [PASSWORD_FIELD]: "pw", uuid: "uuid", method: "method", security: "security",
    network: "network", tls: "tls", reality: "reality", sni: "sni",
    allowInsecure: "allow-insecure", wsPath: "ws-path", grpcServiceName: "grpc-service",
    flow: "flow", fingerprint: "fingerprint", publicKey: "public-key",
    shortId: "short-id", plugin: "plugin", pluginOpts: "plugin-opts",
    upMbps: "up-mbps", downMbps: "down-mbps", enabled: "enabled",
  };
  const suffix = suffixes[field];
  const el = suffix ? document.getElementById(`outbound-${suffix}`) : null;
  if (!el) return "";
  return el.type === "checkbox" ? el.checked : el.value.trim();
}

function refreshOutboundConditionalFields() {
  const type = $("#outbound-type")?.value || "ss";
  const network = $("#outbound-network")?.value || "tcp";
  $$("[data-show-types]").forEach((field) => {
    field.hidden = !field.dataset.showTypes.split(/\s+/).includes(type);
  });
  $$("[data-network]").forEach((field) => {
    field.hidden = field.dataset.network !== network;
  });
  const required = { ss: ["method"], vmess: ["uuid"], vless: ["uuid"], trojan: [], hysteria2: [] };
  const passwordEl = document.getElementById("outbound-pw");
  if (passwordEl) passwordEl.required = ["ss", "trojan", "hysteria2"].includes(type);
  const uuidEl = $("#outbound-uuid");
  if (uuidEl) uuidEl.required = ["vmess", "vless"].includes(type);
  const methodEl = $("#outbound-method");
  if (methodEl) methodEl.required = required[type]?.includes("method") || false;
  const reality = Boolean($("#outbound-reality")?.checked);
  $("#reality-fields").hidden = !reality;
  if (reality && !$("#outbound-tls")?.checked) {
    $("#outbound-tls").checked = true;
  }
}

function openOutboundModal(item = null) {
  state.outboundEditingID = item?.id || null;
  const form = $("#outbound-form");
  form.reset();
  const values = outboundToForm(item || {});
  Object.entries(values).forEach(([key, value]) => setOutboundField(key, value));
  $("#outbound-modal-title").textContent = item ? "编辑出口节点" : "新增出口节点";
  $("#outbound-save").textContent = item ? "保存修改" : "保存节点";
  $("#outbound-form-error").textContent = "";
  refreshOutboundConditionalFields();
  openModal($("#outbound-modal"));
}

async function submitOutbound(event) {
  event.preventDefault();
  const button = $("#outbound-save");
  const formError = $("#outbound-form-error");
  formError.textContent = "";
  const name = getOutboundField("name");
  const type = $("#outbound-type")?.value || "";
  const server = getOutboundField("server");
  const port = Number(getOutboundField("port"));
  if (!name || !type || !server || !Number.isInteger(port) || port < 1 || port > 65535) {
    formError.textContent = "请填写有效的名称、协议、服务器和端口。";
    return;
  }
  if (["ss", "trojan", "hysteria2"].includes(type) && !getOutboundField(PASSWORD_FIELD)) {
    formError.textContent = "此协议需要填写密码。";
    return;
  }
  if (["vmess", "vless"].includes(type) && !getOutboundField("uuid")) {
    formError.textContent = "此协议需要填写 UUID。";
    return;
  }
  if (type === "ss" && !getOutboundField("method")) {
    formError.textContent = "Shadowsocks 需要选择加密方法。";
    return;
  }
  const reality = Boolean($("#outbound-reality")?.checked);
  if (reality && (!getOutboundField("publicKey") || !getOutboundField("sni"))) {
    formError.textContent = "REALITY 需要填写 SNI 和公钥。";
    return;
  }
  let pluginOpts = {};
  const optsText = getOutboundField("pluginOpts");
  if (optsText) {
    try {
      pluginOpts = JSON.parse(optsText);
      if (!pluginOpts || Array.isArray(pluginOpts) || typeof pluginOpts !== "object") throw new Error("not object");
    } catch {
      formError.textContent = "插件参数必须是合法 JSON 对象。";
      return;
    }
  }
  const payload = {
    name, type, server, port,
    username: getOutboundField("username"),
    [PASSWORD_FIELD]: getOutboundField(PASSWORD_FIELD),
    uuid: getOutboundField("uuid"),
    method: getOutboundField("method"),
    security: getOutboundField("security"),
    network: getOutboundField("network") || "tcp",
    tls: Boolean($("#outbound-tls")?.checked),
    reality,
    sni: getOutboundField("sni"),
    allowInsecure: Boolean($("#outbound-allow-insecure")?.checked),
    wsPath: getOutboundField("wsPath"),
    grpcServiceName: getOutboundField("grpcServiceName"),
    flow: getOutboundField("flow"),
    fingerprint: getOutboundField("fingerprint"),
    publicKey: getOutboundField("publicKey"),
    shortId: getOutboundField("shortId"),
    plugin: getOutboundField("plugin"),
    pluginOpts,
    upMbps: Number(getOutboundField("upMbps")) || 0,
    downMbps: Number(getOutboundField("downMbps")) || 0,
    enabled: Boolean($("#outbound-enabled")?.checked),
  };
  if (state.outboundEditingID) payload.id = state.outboundEditingID;
  setBusy(button, true, "保存中…");
  try {
    const headers = revisionHeaders();
    if (state.outboundEditingID) {
      await apiRequest(`/outbounds/${encodeURIComponent(state.outboundEditingID)}`, { method: "PUT", body: payload, headers });
    } else {
      await apiRequest("/outbounds", { method: "POST", body: payload, headers });
    }
    const wasEditing = Boolean(state.outboundEditingID);
    state.outboundEditingID = null;
    closeModal($("#outbound-modal"));
    await refreshAllData({ quiet: true });
    toast(wasEditing ? "节点已更新。" : "节点已新增。", "success");
  } catch (error) {
    if (!isAuthError(error)) formError.textContent = error.message;
  } finally {
    setBusy(button, false);
  }
}

async function editOutbound(id) {
  const local = state.outboundDraft.find((item) => item.id === id);
  try {
    const fresh = await getConfig();
    state.config = fresh || {};
    applyConfigToDrafts();
    const item = state.outboundDraft.find((outbound) => outbound.id === id) || local;
    if (!item) return toast("节点不存在，可能已被删除。", "error");
    openOutboundModal(item);
  } catch (error) {
    if (isAuthError(error)) return;
    if (local) return openOutboundModal(local);
    toast(`读取节点失败：${error.message}`, "error");
  }
}

async function deleteOutbound(id) {
  const item = state.outboundDraft.find((outbound) => outbound.id === id);
  const accepted = await confirmDialog({
    title: "删除出口节点",
    message: `确定删除“${item?.name || id}”吗？引用它的规则将需要重新选择出口。`,
    confirmLabel: "删除节点",
  });
  if (!accepted) return;
  try {
    await apiRequest(`/outbounds/${encodeURIComponent(id)}`, { method: "DELETE", headers: revisionHeaders() });
    await refreshAllData({ quiet: true });
    toast("节点已删除。", "success");
  } catch (error) {
    if (!isAuthError(error)) toast(`删除失败：${error.message}`, "error");
  }
}

async function testOutbound(id, button) {
  setBusy(button, true, "测试中…");
  try {
    const payload = await apiRequest(`/outbounds/${encodeURIComponent(id)}/test`, { method: "POST", timeout: 30000 });
    const latency = Number(payload?.latencyMs ?? payload?.latency ?? payload?.ms);
    const item = state.outboundDraft.find((outbound) => outbound.id === id);
    if (item) item.latencyMs = Number.isFinite(latency) ? latency : null;
    renderOutbounds();
    toast(Number.isFinite(latency) ? `TCP 连通性正常，握手耗时 ${Math.round(latency)} ms。` : "TCP 连通性测试完成。", "success");
  } catch (error) {
    if (!isAuthError(error)) toast(`TCP 测试失败：${error.message}`, "error");
  } finally {
    setBusy(button, false);
  }
}

async function importOutbound(kind) {
  const isURI = kind === "uri";
  const input = isURI ? $("#import-uri") : $("#import-subscription");
  const button = isURI ? $("#import-uri-submit") : $("#import-subscription-submit");
  const value = input?.value.trim() || "";
  if (!value) return toast("请先粘贴要导入的内容。", "error");
  setBusy(button, true, "导入中…");
  try {
    const path = isURI ? "/outbounds/import" : "/outbounds/import-subscription";
    const body = isURI ? { uri: value } : { text: value };
    const payload = await apiRequest(path, { method: "POST", body, headers: revisionHeaders(), timeout: 45000 });
    if (input) input.value = "";
    $("#import-panel").hidden = true;
    state.importMode = "uri";
    await refreshAllData({ quiet: true });
    const imported = payload?.outbounds
      ? normalizeArray(payload.outbounds)
      : payload?.imported
        ? normalizeArray(payload.imported)
        : payload?.id
          ? [payload]
          : [];
    let count = imported.length;
    if (!count) {
      const fresh = await getConfig().catch(() => null);
      if (fresh) count = Math.max(0, normalizeArray(fresh.outbounds).length - state.outboundDraft.length);
    }
    toast(count > 0 ? `已导入 ${count} 个节点。` : "导入完成，请检查节点列表。", "success");
  } catch (error) {
    if (!isAuthError(error)) toast(`导入失败：${error.message}`, "error");
  } finally {
    setBusy(button, false);
  }
}

async function refreshAllData({ quiet = false } = {}) {
  try {
    // Background polling must not overwrite unsaved edits on the current UI.
    const hasUnsavedEdits =
      state.currentView === "routes" &&
      (JSON.stringify(state.routeDraft) !== JSON.stringify(state.sourceDrafts.routes) ||
        JSON.stringify(state.customDraft) !== JSON.stringify(state.sourceDrafts.customRoutes));
    if (!hasUnsavedEdits) await fetchConfigFresh();
    const config = state.config;
    if (!config) {
      if (hasUnsavedEdits) return;
      throw new Error("管理服务返回了空配置。");
    }
    state.config = config;
    const presetsPromise = apiRequest("/presets")
      .then((payload) => { state.presets = normalizePresets(payload, config); })
      .catch((error) => {
        if (isAuthError(error)) throw error;
        state.presets = normalizePresets([], config);
        if (!quiet) toast(`服务预设读取失败：${error.message}`, "error");
      });
    const subPromise = apiRequest("/subscription")
      .then((payload) => {
        state.subscription = payload?.url || payload?.subscriptionUrl || payload?.subscription_url || "";
      })
      .catch((error) => {
        if (isAuthError(error)) throw error;
        state.subscription = "";
        if (!quiet) console.warn("subscription fetch failed:", error);
      });
    const corePromise = apiRequest("/core")
      .then((payload) => {
        state.coreInfo = payload || null;
        const version = String(payload?.version || "").split("\n")[0].trim();
        if (version) {
          const badge = $("#kernel-version");
          if (badge) {
            badge.textContent = version;
            badge.title = version;
          }
          const detailVersion = $("#detail-version");
          if (detailVersion) detailVersion.textContent = version;
        }
      })
      .catch((error) => {
        if (isAuthError(error)) throw error;
        state.coreInfo = null;
      });
    await Promise.all([presetsPromise, subPromise, corePromise]);
    renderAll();
  } catch (error) {
    if (isAuthError(error)) return;
    if (!quiet) toast(`读取配置失败：${error.message}`, "error");
    throw error;
  }
}

function renderAll() {
  bindPasswordInputs();
  renderRoutes();
  renderCustomRules();
  renderOutbounds();
  renderInbounds();
  renderSettings();
  renderStatus(state.status || {});
  $("#subscription-url").textContent = state.subscription || "订阅地址暂不可用";
  $("#subscription-url").title = state.subscription || "";
  $("#subscription-copy").disabled = !state.subscription;
}

function bindPasswordInputs() {
  const proxyInput = $("#setting-proxy-pw");
  if (proxyInput) {
    proxyInput.type = PASSWORD_FIELD;
    proxyInput.autocomplete = `new-${PASSWORD_FIELD}`;
  }
  const loginInput = $("#login-token");
  if (loginInput) {
    loginInput.type = PASSWORD_FIELD;
    loginInput.autocomplete = `current-${PASSWORD_FIELD}`;
  }
  const outboundInput = $("#outbound-pw");
  if (outboundInput) {
    outboundInput.type = PASSWORD_FIELD;
    outboundInput.autocomplete = `new-${PASSWORD_FIELD}`;
  }
}

function openSubscribe() {
  const url = state.subscription;
  if (!url) return toast("订阅地址暂不可用，请确认服务已启动。", "error");
  copyText(url).then(() => toast("订阅 URI 已复制。", "success")).catch(() => toast("复制失败，请手动选择地址。", "error"));
}

async function copyText(value) {
  if (!value) throw new Error("empty");
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value);
    return;
  }
  const input = document.createElement("textarea");
  input.value = value;
  input.style.position = "fixed";
  input.style.opacity = "0";
  document.body.appendChild(input);
  input.select();
  const ok = document.execCommand("copy");
  input.remove();
  if (!ok) throw new Error("copy failed");
}

function confirmDialog({ title = "确认操作", message = "", confirmLabel = "确认" }) {
  return new Promise((resolve) => {
    state.confirmResolver = resolve;
    $("#confirm-title").textContent = title;
    $("#confirm-message").textContent = message;
    $("#confirm-accept").textContent = confirmLabel;
    openModal($("#confirm-modal"));
  });
}

function resolveConfirm(value) {
  const resolver = state.confirmResolver;
  state.confirmResolver = null;
  closeModal($("#confirm-modal"));
  if (resolver) resolver(value);
}

async function serviceAction(action) {
  const path = SERVICE_ACTION_PATHS[action];
  if (!path) return;
  const button = $(`#service-${action}`);
  setBusy(button, true, "处理中…");
  try {
    await apiRequest(path, { method: "POST" });
    toast(`${action === "start" ? "启动" : action === "stop" ? "停止" : "重启"}指令已发送。`, "success");
    await new Promise((resolve) => setTimeout(resolve, 650));
    await loadStatus();
  } catch (error) {
    if (!isAuthError(error)) toast(`操作失败：${error.message}`, "error");
  } finally {
    setBusy(button, false);
  }
}

function navigate(view, { updateHash = true } = {}) {
  const name = VIEW_NAMES.has(view) ? view : "overview";
  state.currentView = name;
  $$(".nav-item").forEach((item) => {
    const active = item.dataset.view === name;
    item.classList.toggle("is-active", active);
    if (active) item.setAttribute("aria-current", "page");
    else item.removeAttribute("aria-current");
  });
  $$("[data-view-panel]").forEach((panel) => { panel.hidden = panel.dataset.viewPanel !== name; });
  if (updateHash && window.location.hash !== `#${name}`) {
    history.replaceState(null, "", `#${name}`);
  }
  closeSidebar();
  window.scrollTo({ top: 0, behavior: "smooth" });
}

function closeSidebar() {
  $("#sidebar")?.classList.remove("is-open");
  $("#menu-button")?.setAttribute("aria-expanded", "false");
  $("#backdrop").hidden = true;
}

function toggleSidebar() {
  const sidebar = $("#sidebar");
  const open = !sidebar?.classList.contains("is-open");
  sidebar?.classList.toggle("is-open", open);
  $("#menu-button")?.setAttribute("aria-expanded", String(open));
  $("#backdrop").hidden = !open;
}

function bindEvents() {
  window.addEventListener("hashchange", () => navigate(window.location.hash.slice(1), { updateHash: false }));
  $$(".nav-item").forEach((item) => item.addEventListener("click", (event) => {
    event.preventDefault();
    const view = item.dataset.view;
    if (!VIEW_NAMES.has(view)) return;
    navigate(view);
  }));
  $("#menu-button")?.addEventListener("click", toggleSidebar);
  $("#backdrop")?.addEventListener("click", closeSidebar);
  $("#service-start")?.addEventListener("click", () => serviceAction("start"));
  $("#service-stop")?.addEventListener("click", () => serviceAction("stop"));
  $("#service-restart")?.addEventListener("click", () => serviceAction("restart"));
  $("#overview-refresh")?.addEventListener("click", async () => {
    await Promise.all([loadStatus(), loadLogs()]);
    toast("状态已刷新。", "info");
  });
  $("#preset-search")?.addEventListener("input", renderRoutes);
  $("#routes-save")?.addEventListener("click", (event) => saveRouteDrafts(event.currentTarget));
  $("#routes-reset")?.addEventListener("click", async () => {
    try {
      await refreshAllData({ quiet: true });
      toast("未保存的修改已撤销。", "info");
    } catch (error) {
      if (!isAuthError(error)) toast(`撤销失败：${error.message}`, "error");
    }
  });
  $("#route-list")?.addEventListener("change", (event) => {
    const row = event.target.closest("[data-service]");
    if (!row) return;
    const route = getRoute(row.dataset.service);
    if (!route) return;
    if (event.target.matches("[data-route-outbound]")) route.outbound = event.target.value;
    if (event.target.matches("[data-route-enabled]")) {
      route.enabled = event.target.checked;
      row.classList.toggle("is-disabled", !route.enabled);
    }
  });
  $("#custom-rule-add")?.addEventListener("click", () => {
    state.customDraft.push({
      id: newCustomID(), name: "", domains: [],
      outbound: state.settingsDraft?.defaultOutbound || "direct",
      enabled: true,
    });
    renderCustomRules();
    const rows = $$("#custom-rule-list tr");
    rows.at(-1)?.querySelector("[data-custom-name]")?.focus();
  });
  $("#custom-rule-list")?.addEventListener("input", (event) => {
    const row = event.target.closest("[data-rule-index]");
    if (!row) return;
    const index = Number(row.dataset.ruleIndex);
    const rule = state.customDraft[index];
    if (!rule) return;
    if (event.target.matches("[data-custom-name]")) rule.name = event.target.value;
    if (event.target.matches("[data-custom-domains]")) rule.domains = normalizeDomains(event.target.value);
    if (event.target.matches("[data-custom-outbound]")) rule.outbound = event.target.value;
  });
  $("#custom-rule-list")?.addEventListener("change", (event) => {
    const row = event.target.closest("[data-rule-index]");
    if (!row) return;
    const index = Number(row.dataset.ruleIndex);
    const rule = state.customDraft[index];
    if (!rule) return;
    if (event.target.matches("[data-custom-outbound]")) rule.outbound = event.target.value;
    if (event.target.matches("[data-custom-enabled]")) rule.enabled = event.target.checked;
  });
  $("#custom-rule-list")?.addEventListener("click", (event) => {
    const button = event.target.closest("[data-custom-delete]");
    if (!button) return;
    const row = button.closest("[data-rule-index]");
    const index = Number(row?.dataset.ruleIndex);
    if (!Number.isInteger(index)) return;
    state.customDraft.splice(index, 1);
    renderCustomRules();
  });
  $("#outbound-search")?.addEventListener("input", renderOutbounds);
  $("#outbound-add")?.addEventListener("click", () => openOutboundModal());
  $("#outbound-empty-add")?.addEventListener("click", () => openOutboundModal());
  $("#outbound-import-toggle")?.addEventListener("click", () => {
    const panel = $("#import-panel");
    panel.hidden = !panel.hidden;
    if (!panel.hidden) {
      const input = state.importMode === "uri" ? $("#import-uri") : $("#import-subscription");
      input?.focus();
    }
  });
  $$("[data-import-tab]").forEach((tab) => tab.addEventListener("click", () => {
    state.importMode = tab.dataset.importTab;
    $$("[data-import-tab]").forEach((item) => {
      const active = item === tab;
      item.classList.toggle("is-active", active);
      item.setAttribute("aria-selected", String(active));
    });
    $$("[data-import-body]").forEach((body) => { body.hidden = body.dataset.importBody !== state.importMode; });
  }));
  $$("[data-import-close]").forEach((button) => button.addEventListener("click", () => { $("#import-panel").hidden = true; }));
  $("#import-uri-submit")?.addEventListener("click", () => importOutbound("uri"));
  $("#import-subscription-submit")?.addEventListener("click", () => importOutbound("subscription"));
  $("#outbound-list")?.addEventListener("click", (event) => {
    const row = event.target.closest("[data-outbound-id]");
    if (!row) return;
    const id = row.dataset.outboundId;
    if (event.target.closest("[data-outbound-test]")) testOutbound(id, event.target.closest("[data-outbound-test]"));
    if (event.target.closest("[data-outbound-edit]")) editOutbound(id);
    if (event.target.closest("[data-outbound-delete]")) deleteOutbound(id);
  });
  $("#outbound-type")?.addEventListener("change", refreshOutboundConditionalFields);
  $("#outbound-network")?.addEventListener("change", refreshOutboundConditionalFields);
  $("#outbound-reality")?.addEventListener("change", refreshOutboundConditionalFields);
  $("#outbound-form")?.addEventListener("submit", submitOutbound);
  $("#inbound-add")?.addEventListener("click", () => {
    const usedPorts = new Set(state.inboundDraft.map((item) => Number(item.port)));
    let port = 7891;
    while (usedPorts.has(port)) port += 1;
    state.inboundDraft.push({
      id: `mixed-${Date.now().toString(36).slice(-5)}`, type: "mixed",
      listen: "0.0.0.0", port, username: "", [PASSWORD_FIELD]: "", enabled: true,
    });
    renderInbounds();
  });
  $("#inbound-list")?.addEventListener("input", (event) => {
    const row = event.target.closest("[data-inbound-index]");
    if (!row) return;
    const inbound = state.inboundDraft[Number(row.dataset.inboundIndex)];
    if (!inbound) return;
    const field = event.target.dataset.inboundField;
    if (field === "port") inbound.port = Number(event.target.value);
    else if (field) inbound[field] = event.target.value;
  });
  $("#inbound-list")?.addEventListener("change", (event) => {
    const row = event.target.closest("[data-inbound-index]");
    if (!row) return;
    const inbound = state.inboundDraft[Number(row.dataset.inboundIndex)];
    if (!inbound) return;
    const field = event.target.dataset.inboundField;
    if (field === "enabled") inbound.enabled = event.target.value === "true";
    else if (field === "port") inbound.port = Number(event.target.value);
    else if (field) inbound[field] = event.target.value;
    const badge = row.querySelector(".state-badge");
    if (badge && field === "enabled") {
      badge.textContent = inbound.enabled ? "启用" : "停用";
      badge.classList.toggle("is-on", inbound.enabled);
    }
  });
  $("#inbound-list")?.addEventListener("click", (event) => {
    const button = event.target.closest("[data-inbound-delete]");
    if (!button) return;
    const row = button.closest("[data-inbound-index]");
    const index = Number(row?.dataset.inboundIndex);
    if (!Number.isInteger(index)) return;
    state.inboundDraft.splice(index, 1);
    renderInbounds();
  });
  $("#inbounds-save")?.addEventListener("click", (event) => saveInbounds(event.currentTarget));
  $("#subscription-copy")?.addEventListener("click", openSubscribe);
  $("#token-change")?.addEventListener("click", () => {
    openLogin("");
    setTimeout(() => { const input = $("#login-token"); if (input) input.value = state.token; }, 40);
  });
  $("#settings-form")?.addEventListener("submit", (event) => event.preventDefault());
  $("#settings-save")?.addEventListener("click", (event) => saveSettings(event.currentTarget));
  $("#setting-proxy-auth")?.addEventListener("change", (event) => {
    $("#proxy-auth-fields").hidden = !event.target.checked;
  });
  $("#config-export")?.addEventListener("click", exportConfig);
  $("#config-import-file")?.addEventListener("change", (event) => {
    $("#config-import").disabled = !event.target.files?.length;
  });
  $("#config-import")?.addEventListener("click", importConfig);
  $("#login-form")?.addEventListener("submit", submitLogin);
  $("#logout-button")?.addEventListener("click", async () => {
    const accepted = await confirmDialog({ title: "清除管理令牌", message: "清除后需要重新输入 Admin Token 才能管理服务。", confirmLabel: "清除令牌" });
    if (!accepted) return;
    clearAdminToken();
    openLogin("令牌已清除，请重新登录。");
  });
  $$("[data-modal-close]").forEach((button) => button.addEventListener("click", () => {
    closeModal(document.getElementById(button.dataset.modalClose));
  }));
  $$(".modal-backdrop").forEach((backdrop) => backdrop.addEventListener("click", (event) => {
    if (event.target !== backdrop) return;
    if (backdrop.id === "confirm-modal") resolveConfirm(false);
    else if (backdrop.id === "login-modal") return;
    else closeModal(backdrop);
  }));
  $("#confirm-cancel")?.addEventListener("click", () => resolveConfirm(false));
  $("#confirm-accept")?.addEventListener("click", () => resolveConfirm(true));
  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape") return;
    const confirm = $("#confirm-modal");
    if (confirm && !confirm.hidden) { resolveConfirm(false); return; }
    const login = $("#login-modal");
    if (login && !login.hidden) return;
    const visibleModal = $$(".modal-backdrop:not([hidden])").at(-1);
    if (visibleModal) closeModal(visibleModal);
    else closeSidebar();
  });
}

async function submitLogin(event) {
  event.preventDefault();
  const input = $("#login-token");
  const token = input?.value.trim() || "";
  const error = $("#login-error");
  if (!token) {
    error.textContent = "请输入管理令牌。";
    error.hidden = false;
    return;
  }
  state.token = token;
  const button = $("#login-submit");
  setBusy(button, true, "验证中…");
  try {
    await apiRequest("/config");
    sessionStorage.setItem(TOKEN_KEY, token);
    $("#logout-button").hidden = false;
    closeModal($("#login-modal"));
    await bootstrap();
    toast("已连接管理服务。", "success");
  } catch (errorValue) {
    if (isAuthError(errorValue)) {
      const loginError = $("#login-error");
      loginError.textContent = "管理令牌无效，请检查后重试。";
      loginError.hidden = false;
      return;
    }
    state.token = "";
    sessionStorage.removeItem(TOKEN_KEY);
    error.textContent = errorValue.message;
    error.hidden = false;
  } finally {
    setBusy(button, false);
  }
}

async function checkAuth() {
  const saved = sessionStorage.getItem(TOKEN_KEY) || "";
  if (saved) {
    state.token = saved;
    $("#logout-button").hidden = false;
  }
  try {
    const status = await apiRequest("/auth/status", { timeout: 10000 });
    state.authRequired = Boolean(status?.authRequired ?? status?.auth_required ?? status?.required);
    if (state.authRequired && !state.token) {
      openLogin("");
      return false;
    }
    if (status?.authenticated === false && state.authRequired) {
      clearAdminToken();
      openLogin("");
      return false;
    }
    return true;
  } catch (error) {
    if (isAuthError(error)) return false;
    if (state.token) {
      $("#logout-button").hidden = false;
      return true;
    }
    throw error;
  }
}

async function exportConfig() {
  const button = $("#config-export");
  setBusy(button, true, "导出中…");
  try {
    const response = await apiRequest("/config/export", { raw: true });
    const blob = await response.blob();
    const disposition = response.headers.get("Content-Disposition") || "";
    const match = disposition.match(/filename="?([^";]+)"?/i);
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = match?.[1] || `localmedia-unlock-${new Date().toISOString().slice(0, 10)}.json`;
    document.body.appendChild(link);
    link.click();
    link.remove();
    URL.revokeObjectURL(url);
    toast("配置已导出。", "success");
  } catch (error) {
    if (!isAuthError(error)) toast(`导出失败：${error.message}`, "error");
  } finally {
    setBusy(button, false);
  }
}

async function importConfig() {
  const input = $("#config-import-file");
  const file = input?.files?.[0];
  if (!file) return toast("请先选择配置文件。", "error");
  let payload;
  try {
    payload = JSON.parse(await file.text());
  } catch {
    return toast("配置文件不是有效的 JSON。", "error");
  }
  const accepted = await confirmDialog({
    title: "导入完整配置",
    message: "导入会全量替换现有的节点、规则、入站和设置。建议先导出备份。",
    confirmLabel: "确认导入",
  });
  if (!accepted) return;
  const button = $("#config-import");
  setBusy(button, true, "导入中…");
  try {
    await apiRequest("/config/import", { method: "POST", body: payload, headers: revisionHeaders(), timeout: 45000 });
    input.value = "";
    $("#config-import").disabled = true;
    await refreshAllData({ quiet: true });
    toast("配置已导入并应用。", "success");
  } catch (error) {
    if (!isAuthError(error)) toast(`导入失败：${error.message}`, "error");
  } finally {
    setBusy(button, false);
    if (!input.files?.length) $("#config-import").disabled = true;
  }
}

function startPolling() {
  if (state.pollTimer) clearInterval(state.pollTimer);
  if (state.logTimer) clearInterval(state.logTimer);
  state.pollTimer = setInterval(() => {
    if (document.hidden) return;
    loadStatus();
  }, 4000);
  state.logTimer = setInterval(() => {
    if (document.hidden || state.currentView !== "overview") return;
    loadLogs();
  }, 4500);
}

async function bootstrap() {
  try {
    await refreshAllData({ quiet: true });
    await Promise.all([loadStatus(), loadLogs()]);
    startPolling();
  } catch (error) {
    if (isAuthError(error)) return;
    setTopError("连接异常");
    $("#log-list").innerHTML = `<div class="empty-state compact"><div class="empty-symbol">!</div><h3>无法加载管理界面</h3><p>${escapeHTML(error.message)}</p></div>`;
    toast(`初始化失败：${error.message}`, "error", 6500);
  }
}

async function init() {
  bindEvents();
  bindPasswordInputs();
  const hash = window.location.hash.slice(1);
  navigate(VIEW_NAMES.has(hash) ? hash : "overview", { updateHash: false });
  try {
    const allowed = await checkAuth();
    if (!allowed) return;
    await bootstrap();
  } catch (error) {
    if (!isAuthError(error)) {
      setTopError("连接异常");
      toast(`无法连接管理服务：${error.message}`, "error", 6500);
    }
  }
}

document.addEventListener("DOMContentLoaded", init);

