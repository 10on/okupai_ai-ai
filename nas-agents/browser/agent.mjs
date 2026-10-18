// Local browser agent for aimode-mcp's local_browse tool.
//
// The cloud agent sends ONE high-level instruction; this script does the low-level
// work with a headless Chromium on the NAS and the local model on the Mac as the
// decision-maker (vision when the active profile has it), then prints a compact
// JSON report. Raw HTML, DOM dumps and screenshots never leave the NAS/Mac.
//
// stdin:  {instruction, url?, max_steps, extract?, allow_submit, vision,
//          api_base, api_key, session_dir, log_file}
// stdout: {status, url, title, summary, elements, data, tried, steps}

import { chromium } from "playwright-core";
import fs from "node:fs";
import path from "node:path";

const input = JSON.parse(fs.readFileSync(0, "utf8"));
const maxSteps = input.max_steps || 8;

// Clicks/keys that commit something. Without allow_submit they escalate to the
// cloud as need_decision instead of being executed.
const RISKY = /\b(pay|buy|purchase|checkout|order|subscribe|sign ?in|log ?in|login|sign ?up|register|submit|delete|remove|confirm|send|transfer|book)\b|оплат|купить|заказ|войти|регистр|удалить|отправить|подтверд/i;

const SYSTEM = `You control a web browser to fulfil one instruction. Each turn you get the page
state: URL, title, numbered interactive elements [id] and visible text (and a screenshot if available).
Reply with ONE JSON object:
{"thought": "<one short sentence>",
 "action": "click" | "type" | "select" | "press" | "scroll" | "goto" | "back" | "wait" | "done" | "blocked" | "need_decision",
 "id": <element id for click/type/select/press>,
 "text": "<text to type, option to select, or key to press e.g. Enter>",
 "url": "<for goto>",
 "summary": "<3-7 short lines describing the current page, required for done/blocked/need_decision>",
 "data": <extracted JSON when the instruction asks for data, else null>,
 "reason": "<why, for blocked/need_decision>"}
Rules:
- Prefer the most direct element. Never invent ids.
- Use "done" as soon as the instruction is fulfilled; put requested data in "data".
- Use "need_decision" for: several plausible targets, unexpected page, captcha, login/2FA, payment,
  anything destructive or irreversible.
- Use "blocked" if the page cannot be used for this instruction.`;

function log(entry) {
  try {
    fs.appendFileSync(input.log_file, JSON.stringify({ ts: new Date().toISOString(), ...entry }) + "\n");
  } catch {}
}

async function chat(messages) {
  const headers = { "Content-Type": "application/json" };
  if (input.api_key) headers.Authorization = `Bearer ${input.api_key}`;
  const res = await fetch(`${input.api_base}/chat/completions`, {
    method: "POST",
    headers,
    body: JSON.stringify({
      model: "aimode",
      messages,
      max_tokens: 1500,
      temperature: 0.2,
      response_format: { type: "json_object" },
      chat_template_kwargs: { enable_thinking: false },
    }),
    signal: AbortSignal.timeout(300_000),
  });
  const body = await res.json();
  if (!res.ok || !body.choices?.length) throw new Error(`model HTTP ${res.status}: ${JSON.stringify(body).slice(0, 300)}`);
  let text = body.choices[0].message.content || "";
  const think = text.indexOf("</think>");
  if (think >= 0) text = text.slice(think + 8);
  const start = text.indexOf("{");
  const end = text.lastIndexOf("}");
  return JSON.parse(text.slice(start, end + 1));
}

// observe tags visible interactive elements with data-aid and returns a compact view.
async function observe(page) {
  const view = await page.evaluate(() => {
    const sel = 'a[href], button, input:not([type=hidden]), select, textarea, [role=button], [role=link], [role=tab], [role=menuitem], [role=checkbox], [contenteditable=true], summary';
    const els = [];
    let id = 0;
    for (const el of document.querySelectorAll(sel)) {
      const r = el.getBoundingClientRect();
      const st = getComputedStyle(el);
      if (r.width < 2 || r.height < 2 || st.visibility === "hidden" || st.display === "none") continue;
      if (id >= 80) break;
      id++;
      el.setAttribute("data-aid", String(id));
      const label = (el.getAttribute("aria-label") || el.innerText || el.value || el.getAttribute("placeholder") ||
        el.getAttribute("title") || el.getAttribute("name") || el.getAttribute("href") || "").trim().replace(/\s+/g, " ").slice(0, 80);
      els.push({ id, tag: el.tagName.toLowerCase(), type: el.getAttribute("type") || "", label,
        inViewport: r.top < innerHeight && r.bottom > 0 });
    }
    const text = (document.body?.innerText || "").replace(/\n{3,}/g, "\n\n").slice(0, 6000);
    return { els, text };
  });
  return { url: page.url(), title: await page.title(), ...view };
}

function stateText(obs) {
  const els = obs.els.map((e) => `[${e.id}] ${e.tag}${e.type ? ":" + e.type : ""} "${e.label}"${e.inViewport ? "" : " (offscreen)"}`).join("\n");
  return `URL: ${obs.url}\nTITLE: ${obs.title}\n\nELEMENTS:\n${els}\n\nVISIBLE TEXT:\n${obs.text}`;
}

function risky(el, action) {
  if (input.allow_submit || !el) return false;
  if (action === "type" && el.type === "password") return true;
  if (action === "click" && (el.type === "submit" || RISKY.test(el.label))) return true;
  return false;
}

function report(status, obs, decision, tried, steps, extra = {}) {
  const summary = (decision?.summary || "").split("\n").slice(0, 7).join("\n").slice(0, 700);
  let data = decision?.data ?? null;
  if (data && JSON.stringify(data).length > 4000) data = { truncated: JSON.stringify(data).slice(0, 4000) };
  return {
    status,
    url: obs?.url,
    title: obs?.title,
    summary,
    elements: (obs?.els || []).filter((e) => e.inViewport && e.label).slice(0, 10).map((e) => `[${e.id}] ${e.label.slice(0, 40)}`),
    data,
    reason: extra.reason || decision?.reason,
    tried: tried.slice(-8),
    steps,
  };
}

// Use playwright-core's own browser if present, else the newest cached Chromium
// (other tools on the NAS keep ~/.cache/ms-playwright populated at their revision).
function chromiumPath() {
  if (process.env.AIMODE_CHROMIUM) return process.env.AIMODE_CHROMIUM;
  if (fs.existsSync(chromium.executablePath())) return undefined;
  const cache = path.join(process.env.HOME || "", ".cache", "ms-playwright");
  const dirs = fs.existsSync(cache) ? fs.readdirSync(cache) : [];
  const candidates = [
    ...dirs.filter((d) => d.startsWith("chromium_headless_shell-")).map((d) => path.join(cache, d, "chrome-headless-shell-linux64", "chrome-headless-shell")),
    ...dirs.filter((d) => /^chromium-\d+$/.test(d)).map((d) => path.join(cache, d, "chrome-linux64", "chrome")),
  ].filter((p) => fs.existsSync(p));
  candidates.sort((a, b) => Number(b.match(/-(\d+)\//)[1]) - Number(a.match(/-(\d+)\//)[1]));
  return candidates[0];
}

const lastUrlFile = path.join(input.session_dir, "last-url");
fs.mkdirSync(input.session_dir, { recursive: true });
const ctx = await chromium.launchPersistentContext(input.session_dir, {
  headless: true,
  executablePath: chromiumPath(),
  viewport: { width: 1280, height: 800 },
});
const page = ctx.pages()[0] || (await ctx.newPage());
let out;
try {
  let start = input.url;
  if (!start && fs.existsSync(lastUrlFile)) start = fs.readFileSync(lastUrlFile, "utf8").trim();
  if (!start) throw new Error("no url given and no previous page in this session");
  await page.goto(start, { waitUntil: "domcontentloaded", timeout: 45_000 });
  await page.waitForLoadState("networkidle", { timeout: 5_000 }).catch(() => {});

  const tried = [];
  const history = [];
  let failures = 0;
  let obs, decision;
  for (let step = 1; step <= maxSteps + 1; step++) {
    obs = await observe(page);
    if (step > maxSteps) {
      out = report("blocked", obs, decision, tried, step - 1, { reason: `step cap (${maxSteps}) reached` });
      break;
    }
    const goal = `INSTRUCTION: ${input.instruction}${input.extract ? `\nEXTRACT: ${input.extract}` : ""}`;
    const content = [{ type: "text", text: `${goal}\n\nPREVIOUS ACTIONS:\n${history.slice(-6).join("\n") || "(none)"}\n\n${stateText(obs)}` }];
    if (input.vision) {
      const shot = await page.screenshot({ type: "jpeg", quality: 60 });
      content.push({ type: "image_url", image_url: { url: `data:image/jpeg;base64,${shot.toString("base64")}` } });
    }
    decision = await chat([{ role: "system", content: SYSTEM }, { role: "user", content }]);
    const act = decision.action;
    log({ step, url: obs.url, action: act, id: decision.id, text: decision.text, thought: decision.thought });
    if (act === "done" || act === "blocked" || act === "need_decision") {
      out = report(act, obs, decision, tried, step);
      break;
    }
    const el = obs.els.find((e) => e.id === Number(decision.id));
    if (risky(el, act)) {
      out = report("need_decision", obs, decision, tried, step,
        { reason: `"${act}" on "${el.label}" looks like a commit action (login/submit/payment); needs explicit user OK (allow_submit)` });
      break;
    }
    const loc = el ? page.locator(`[data-aid="${el.id}"]`) : null;
    let desc = `${act}${el ? ` [${el.id}] "${el.label.slice(0, 40)}"` : ""}${decision.text ? ` "${String(decision.text).slice(0, 40)}"` : ""}${decision.url ? ` ${decision.url}` : ""}`;
    try {
      switch (act) {
        case "click": await loc.click({ timeout: 8_000 }); break;
        case "type": await loc.fill(String(decision.text ?? ""), { timeout: 8_000 }); break;
        case "select": await loc.selectOption({ label: String(decision.text) }, { timeout: 8_000 }); break;
        case "press":
          if (!input.allow_submit && el && el.type !== "search" && decision.text === "Enter" && !/search|поиск|q\b/i.test(el.label)) {
            out = report("need_decision", obs, decision, tried, step, { reason: "pressing Enter would submit a form; needs allow_submit" });
            break;
          }
          await (loc ? loc.press(String(decision.text || "Enter")) : page.keyboard.press(String(decision.text || "Enter")));
          break;
        case "scroll": await page.mouse.wheel(0, 700); break;
        case "goto": await page.goto(decision.url, { waitUntil: "domcontentloaded", timeout: 45_000 }); break;
        case "back": await page.goBack({ timeout: 15_000 }); break;
        case "wait": await page.waitForTimeout(2_000); break;
        default: throw new Error(`unknown action ${act}`);
      }
      if (out) break;
      await page.waitForLoadState("domcontentloaded", { timeout: 10_000 }).catch(() => {});
      await page.waitForLoadState("networkidle", { timeout: 4_000 }).catch(() => {});
      failures = 0;
      desc += " -> ok";
    } catch (e) {
      failures++;
      desc += ` -> failed: ${String(e.message).split("\n")[0].slice(0, 100)}`;
      if (failures >= 2) {
        obs = await observe(page);
        out = report("blocked", obs, decision, [...tried, desc], step, { reason: "2 failed attempts in a row" });
        break;
      }
    }
    tried.push(desc);
    history.push(`${step}. ${desc}`);
    log({ step, result: desc });
  }
  fs.writeFileSync(lastUrlFile, page.url());
} catch (e) {
  out = { status: "blocked", url: page.url(), reason: String(e.message).split("\n")[0].slice(0, 300) };
} finally {
  await ctx.close();
}
process.stdout.write(JSON.stringify(out, null, 1) + "\n");
