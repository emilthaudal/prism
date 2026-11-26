#!/usr/bin/env python3
"""Token and cost baseline from Claude Code transcripts (read-only).

Usage: python3 -I baseline.py [--since YYYY-MM-DD] [--until YYYY-MM-DD] [--root ~/.claude/projects]
           [--json out.json]
Days are local-time calendar days; --until is inclusive.
$ is a relative weight from list prices, not a bill.
"""
import argparse, glob, json, os, re, statistics, sys
from collections import Counter, defaultdict
from datetime import datetime, date, timedelta

# per MTok: input, output, cache read
PRICES = {
    "claude-opus-5-5": (4, 20, 0.20),
    "claude-sonnet-5-5": (2, 10, 0.10),
    "claude-haiku-5-5": (0.10, 0.50, 0.01),
    "claude-fable-5-2": (8, 40, 0.20),
    "claude-opus-5": (5, 25, 0.50),
    "claude-sonnet-5": (2, 10, 0.20),
}
SHORT = {"claude-opus-5-5": "opus-5.5", "claude-sonnet-5-5": "sonnet-5.5", "claude-haiku-5-5": "haiku-5.5",
         "claude-fable-5-2": "fable-5.2", "claude-opus-5": "opus-5", "claude-sonnet-5": "sonnet-5"}
CARD = ".local/state/tracks/cards"
BUILTIN = re.compile(r"<command-name>/?(clear|model|effort|compact|resume|config|login|status|cost|context|mcp|exit|rename|color)</command-name>")


def components(model, u):
    """$ split: input, cache write 5m, cache write 1h, cache read, output."""
    p = PRICES.get(model)
    if not p:
        return (0, 0, 0, 0, 0)
    pin, pout, pcr = p
    cc = u.get("cache_creation") or {}
    w5, w1 = cc.get("ephemeral_5m_input_tokens"), cc.get("ephemeral_1h_input_tokens")
    if w5 is None and w1 is None:
        w5, w1 = u.get("cache_creation_input_tokens") or 0, 0
    return ((u.get("input_tokens") or 0) * pin / 1e6, (w5 or 0) * pin * 1.25 / 1e6, (w1 or 0) * pin * 2 / 1e6,
            (u.get("cache_read_input_tokens") or 0) * pcr / 1e6, (u.get("output_tokens") or 0) * pout / 1e6)


def cost(model, u):
    p = PRICES.get(model)
    if not p:
        return 0.0
    pin, pout, pcr = p
    cc = u.get("cache_creation") or {}
    w5 = cc.get("ephemeral_5m_input_tokens")
    w1 = cc.get("ephemeral_1h_input_tokens")
    cw = u.get("cache_creation_input_tokens") or 0
    if w5 is None and w1 is None:
        w5, w1 = cw, 0
    w5, w1 = w5 or 0, w1 or 0
    return ((u.get("input_tokens") or 0) * pin + (u.get("output_tokens") or 0) * pout
            + (u.get("cache_read_input_tokens") or 0) * pcr + w5 * pin * 1.25 + w1 * pin * 2) / 1e6


def ts(s):
    try:
        return datetime.fromisoformat(s.replace("Z", "+00:00")).astimezone()
    except Exception:
        return None


def user_text(msg):
    c = (msg or {}).get("content")
    if isinstance(c, str):
        return c
    if isinstance(c, list):
        if any(isinstance(b, dict) and b.get("type") == "tool_result" for b in c):
            return None
        return " ".join(b.get("text", "") for b in c if isinstance(b, dict) and b.get("type") == "text")
    return None


class Sess:
    def __init__(self, sid, path):
        self.sid, self.path = sid, path
        self.cwd = None
        self.first_prompt = None
        self.reqs = []          # (ts, model, usage, cost, effort, cachettl)
        self.subagents = []     # Sess
        self.compacts = 0
        self.tools = Counter()
        self.card_calls = 0
        self.card_out_est = 0
        self.card_msg_out = 0
        self.meta = {}

    def tot(self):
        t = Counter()
        for _, m, u, c, *_ in self.reqs:
            t["req"] += 1
            t["in"] += u.get("input_tokens") or 0
            t["cw"] += u.get("cache_creation_input_tokens") or 0
            t["cr"] += u.get("cache_read_input_tokens") or 0
            t["out"] += u.get("output_tokens") or 0
            t["cost"] += c
        return t


def parse(path, since, until, seen):
    s = Sess(os.path.basename(path)[:-6], path)
    msgs = {}  # id -> entry with largest output
    order = []
    with open(path, errors="replace") as fh:
        for line in fh:
            try:
                d = json.loads(line)
            except Exception:
                continue
            t = d.get("type")
            if s.cwd is None and d.get("cwd"):
                s.cwd = d["cwd"]
            if t == "user" and s.first_prompt is None and not d.get("isMeta") and not d.get("toolUseResult"):
                txt = user_text(d.get("message"))
                if txt and txt.strip() and not BUILTIN.search(txt) and not txt.lstrip().startswith("<local-command"):
                    s.first_prompt = txt.strip()
            elif t == "system" and d.get("subtype") == "compact_boundary":
                dt = ts(d.get("timestamp", ""))
                if dt and since <= dt.date() <= until:
                    s.compacts += 1
            elif t == "assistant":
                m = d.get("message") or {}
                mid = m.get("id")
                if not mid or m.get("model") == "<synthetic>":
                    continue
                dt = ts(d.get("timestamp", ""))
                if not dt or not (since <= dt.date() <= until):
                    continue
                # tool uses (blocks are split across entries sharing the id)
                for b in m.get("content") or []:
                    if isinstance(b, dict) and b.get("type") == "tool_use":
                        if (mid, b.get("id")) in seen:
                            continue
                        seen.add((mid, b.get("id")))
                        name = b.get("name", "?")
                        s.tools[name] += 1
                        inp = b.get("input") or {}
                        js = json.dumps(inp)
                        hit = False
                        if name in ("Write", "Edit") and CARD in str(inp.get("file_path", "")):
                            hit = True
                        elif name == "Bash" and CARD in str(inp.get("command", "")) and re.search(r"\bmv\b", str(inp.get("command", ""))):
                            hit = True
                        if hit:
                            s.card_calls += 1
                            s.card_kinds = getattr(s, "card_kinds", Counter())
                            s.card_kinds[name] += 1
                            s.card_out_est += len(js) / 3.5
                            s.card_msg_ids = getattr(s, "card_msg_ids", set()) | {mid}
                u = m.get("usage") or {}
                prev = msgs.get(mid)
                if prev is None:
                    order.append(mid)
                if prev is None or (u.get("output_tokens") or 0) >= (prev[2].get("output_tokens") or 0):
                    msgs[mid] = (dt, m.get("model"), u, d.get("effort"), d.get("promptCacheRequested"))
    for mid in order:
        if mid in seen:  # already attributed in another file
            continue
        seen.add(mid)
        dt, model, u, eff, ttl = msgs[mid]
        s.reqs.append((dt, model, u, cost(model, u), eff, ttl))
        if mid in getattr(s, "card_msg_ids", ()):
            s.card_msg_out += u.get("output_tokens") or 0
    s.reqs.sort(key=lambda r: r[0])
    return s


def fmt(n):
    n = float(n)
    for unit, div in (("B", 1e9), ("M", 1e6), ("k", 1e3)):
        if abs(n) >= div:
            return f"{n/div:.1f}{unit}"
    return f"{n:.0f}"


def table(rows, headers):
    w = [max(len(str(h)), *(len(str(r[i])) for r in rows)) if rows else len(str(h)) for i, h in enumerate(headers)]
    out = ["  ".join(str(h).ljust(w[i]) for i, h in enumerate(headers)),
           "  ".join("-" * w[i] for i in range(len(headers)))]
    for r in rows:
        out.append("  ".join(str(c).rjust(w[i]) if i else str(c).ljust(w[i]) for i, c in enumerate(r)))
    return "\n".join(out)


def main():
    ap = argparse.ArgumentParser()
    today = date.today()
    ap.add_argument("--since", default=str(today - timedelta(days=13)))
    ap.add_argument("--until", default=str(today))
    ap.add_argument("--root", default=os.path.expanduser("~/.claude/projects"))
    ap.add_argument("--json")
    a = ap.parse_args()
    since, until = date.fromisoformat(a.since), date.fromisoformat(a.until)
    cutoff = datetime.combine(since, datetime.min.time()).timestamp()

    seen = set()
    sessions = {}
    # subagent files first so shared message ids get attributed to the subagent
    subfiles = [f for f in glob.glob(os.path.join(a.root, "*", "*", "subagents", "*.jsonl")) if os.path.getmtime(f) >= cutoff]
    mainfiles = [f for f in glob.glob(os.path.join(a.root, "*", "*.jsonl")) if os.path.getmtime(f) >= cutoff]
    subs = []
    for f in subfiles:
        sa = parse(f, since, until, seen)
        mp = f[:-6] + ".meta.json"
        if os.path.exists(mp):
            try:
                sa.meta = json.load(open(mp))
            except Exception:
                pass
        sa.parent = f.split(os.sep)[-3]
        subs.append(sa)
    for f in mainfiles:
        s = parse(f, since, until, seen)
        sessions[s.sid] = s
    for sa in subs:
        p = sessions.get(sa.parent)
        if p is None:
            p = sessions[sa.parent] = Sess(sa.parent, None)
        p.subagents.append(sa)
    allsess = [s for s in sessions.values() if s.reqs or any(x.reqs for x in s.subagents)]
    out = []
    P = out.append
    P(f"# Baseline {since} .. {until}  (sessions with activity: {len(allsess)}, subagent transcripts: {sum(1 for x in subs if x.reqs)})")

    # 1. per day per model
    agg = defaultdict(Counter)
    def add(key, r):
        _, m, u, c, *_ = r
        k = agg[key]
        k["req"] += 1; k["in"] += u.get("input_tokens") or 0; k["cw"] += u.get("cache_creation_input_tokens") or 0
        k["cr"] += u.get("cache_read_input_tokens") or 0; k["out"] += u.get("output_tokens") or 0; k["cost"] += c
    for s in allsess:
        for r in s.reqs:
            add(("day", r[0].date(), r[1]), r); add(("dayt", r[0].date()), r); add(("model", r[1]), r); add(("main",), r); add(("all",), r)
        for sa in s.subagents:
            typ = sa.meta.get("agentType", "?")
            for r in sa.reqs:
                add(("day", r[0].date(), r[1]), r); add(("dayt", r[0].date()), r); add(("model", r[1]), r); add(("sub",), r); add(("all",), r)
                add(("subtype", typ, r[1]), r)
    def row(lbl, k):
        return [lbl, k["req"], fmt(k["in"]), fmt(k["cw"]), fmt(k["cr"]), fmt(k["out"]), f"{k['cost']:.0f}"]
    H = ["", "req", "input", "cache_w", "cache_r", "output", "$"]
    P("\n## 1. Per day (all models)")
    days = sorted({k[1] for k in agg if k[0] == "dayt"})
    P(table([row(str(d), agg[("dayt", d)]) for d in days] + [row("TOTAL", agg[("all",)])], H))
    P("\n## 1b. Per model (window)")
    ms = sorted({k[1] for k in agg if k[0] == "model"}, key=lambda m: -agg[("model", m)]["cost"])
    P(table([row(SHORT.get(m, m), agg[("model", m)]) for m in ms], H))
    P("\n## 1c. Per day x model")
    rows = []
    for d in days:
        for m in ms:
            k = agg.get(("day", d, m))
            if k:
                rows.append(row(f"{d} {SHORT.get(m, m)}", k))
    P(table(rows, H))

    # 2. main vs sub
    P("\n## 2. Main session vs subagent")
    P(table([row("main", agg[("main",)]), row("subagent", agg[("sub",)])], H))
    P("\n## 2b. Subagent by type x model")
    st = sorted({(k[1], k[2]) for k in agg if k[0] == "subtype"}, key=lambda x: -agg[("subtype",) + x]["cost"])
    nagents = Counter()
    for s in allsess:
        for sa in s.subagents:
            if sa.reqs:
                nagents[(sa.meta.get("agentType", "?"), sa.reqs[0][1])] += 1
    P(table([[f"{t} / {SHORT.get(m, m)}", nagents.get((t, m), "")] + row("", agg[("subtype", t, m)])[1:] for t, m in st],
            ["type / model", "agents"] + H[1:]))

    # 3. cache health
    P("\n## 3. Cache health (main transcripts; hit = cr/(cr+cw+in))")
    ch = []
    for s in allsess:
        for x in [s] + s.subagents:
            t = x.tot()
            den = t["cr"] + t["cw"] + t["in"]
            if t["req"] >= 5 and den:
                ch.append((t["cr"] / den, x, t))
    tot_all = agg[("all",)]
    P(f"overall hit rate: {tot_all['cr']/(tot_all['cr']+tot_all['cw']+tot_all['in']):.3f}; "
      f"cache_write $ share: {sum(cost(r[1], {'cache_creation_input_tokens': r[2].get('cache_creation_input_tokens'), 'cache_creation': r[2].get('cache_creation')}) for s in allsess for x in [s]+s.subagents for r in x.reqs)/tot_all['cost']:.2%}")
    hits = sorted(h for h, *_ in ch)
    if hits:
        P(f"per-transcript hit rate (>=5 req): median {statistics.median(hits):.3f}, p10 {hits[len(hits)//10]:.3f}, n={len(hits)}")
    def reasons(x):
        r = []
        models = [q[1] for q in x.reqs]
        sw = sum(1 for i in range(1, len(models)) if models[i] != models[i - 1])
        if sw: r.append(f"model switches {sw}")
        effs = [q[4] for q in x.reqs]
        ec = sum(1 for i in range(1, len(effs)) if effs[i] != effs[i - 1])
        if ec: r.append(f"effort changes {ec}")
        if x.compacts: r.append(f"/compact {x.compacts}")
        gaps = 0
        for i in range(1, len(x.reqs)):
            ttl = 3600 if (x.reqs[i][5] == "1h") else 300
            if (x.reqs[i][0] - x.reqs[i - 1][0]).total_seconds() > ttl:
                gaps += 1
        if gaps: r.append(f"idle>TTL {gaps}")
        ttls = Counter(q[5] for q in x.reqs)
        if ttls.get("5m"): r.append(f"5m-TTL reqs {ttls['5m']}")
        return ", ".join(r) or "short session / large first write"
    worst = sorted([c for c in ch if c[2]["cw"] + c[2]["cr"] > 1e6], key=lambda c: c[0])[:10]
    P(table([[x.sid[:8] + ("*" if x.path and "/subagents/" in x.path else ""), f"{h:.3f}", t["req"], fmt(t["cw"]), fmt(t["cr"]),
              f"{t['cost']:.1f}", reasons(x)] for h, x, t in worst],
            ["transcript (*=sub)", "hit", "req", "cache_w", "cache_r", "$", "likely reason"]))

    # 4. fixed overhead
    comp = [0.0] * 5
    rebuild = Counter()
    for s in allsess:
        for x in [s] + s.subagents:
            for i, r in enumerate(x.reqs):
                c = components(r[1], r[2])
                comp = [a_ + b_ for a_, b_ in zip(comp, c)]
                if i:
                    ttl = 3600 if r[5] == "1h" else 300
                    if (r[0] - x.reqs[i - 1][0]).total_seconds() > ttl:
                        rebuild["tok"] += r[2].get("cache_creation_input_tokens") or 0
                        rebuild["cost"] += c[1] + c[2]
                        rebuild["n"] += 1
                        rebuild["cost_" + ("main" if x is s else "sub")] += c[1] + c[2]
    tc_ = sum(comp) or 1
    P("$ by component: " + ", ".join(f"{n} {v:.0f} ({v/tc_:.0%})" for n, v in
                                    zip(["input", "write5m", "write1h", "read", "output"], comp)))
    P(f"cache rebuilds after idle>TTL: {rebuild['n']} requests, {fmt(rebuild['tok'])} written, ${rebuild['cost']:.0f} "
      f"(main ${rebuild['cost_main']:.0f}, sub ${rebuild['cost_sub']:.0f})")
    ttlc = Counter((x is s, r[5]) for s in allsess for x in [s] + s.subagents for r in x.reqs)
    P("TTL requested (main/sub): " + ", ".join(f"{'main' if k[0] else 'sub'} {k[1]}: {v}" for k, v in sorted(ttlc.items(), key=str)))

    P("\n## 4. Fixed overhead: first request context (in+cw+cr) of each main session")
    fo = defaultdict(list)
    fom = defaultdict(list)
    for s in allsess:
        if s.reqs:
            dt, m, u, *_ = s.reqs[0]
            ctx = (u.get("input_tokens") or 0) + (u.get("cache_creation_input_tokens") or 0) + (u.get("cache_read_input_tokens") or 0)
            fo[dt.date()].append(ctx); fom[SHORT.get(m, m)].append(ctx)
    allfo = [v for l in fo.values() for v in l]
    P(table([[str(d), len(fo[d]), fmt(statistics.median(fo[d])), fmt(min(fo[d])), fmt(max(fo[d]))] for d in sorted(fo)]
            + [["ALL", len(allfo), fmt(statistics.median(allfo)) if allfo else 0, fmt(min(allfo)) if allfo else 0, fmt(max(allfo)) if allfo else 0]],
            ["day", "sessions", "median", "min", "max"]))
    P(table([[m, len(v), fmt(statistics.median(v))] for m, v in fom.items()], ["first model", "sessions", "median"]))
    sfo = [ (sa.reqs[0][2].get("input_tokens") or 0)+(sa.reqs[0][2].get("cache_creation_input_tokens") or 0)+(sa.reqs[0][2].get("cache_read_input_tokens") or 0)
            for s in allsess for sa in s.subagents if sa.reqs]
    if sfo:
        P(f"subagent first-request context: median {fmt(statistics.median(sfo))}, n={len(sfo)}")
    # tax: the first-request prefix is written once and re-read on every later request of that transcript
    tax_w = tax_r = 0.0
    for s in allsess:
        for x in [s] + s.subagents:
            if not x.reqs:
                continue
            m, u0 = x.reqs[0][1], x.reqs[0][2]
            pfx = (u0.get("input_tokens") or 0) + (u0.get("cache_creation_input_tokens") or 0) + (u0.get("cache_read_input_tokens") or 0)
            p = PRICES.get(m, (0, 0, 0))
            ttlm = 2 if x.reqs[0][5] == "1h" else 1.25
            tax_w += pfx * p[0] * ttlm / 1e6
            tax_r += sum(pfx * PRICES.get(r[1], (0, 0, 0))[2] for r in x.reqs[1:]) / 1e6
    P(f"prefix tax (first-request context, written once + re-read every request): write ${tax_w:.0f} + read ${tax_r:.0f} "
      f"= ${tax_w+tax_r:.0f} ({(tax_w+tax_r)/agg[('all',)]['cost']:.0%} of total); every 10k cut ≈ "
      f"${(tax_w+tax_r)/ (statistics.median(allfo + sfo) / 1e4):.0f} per window")

    # 5. track sessions
    P("\n## 5. Track sessions: card writes/moves")
    tr = [s for s in allsess if s.first_prompt and (s.first_prompt.startswith("/track") or "<command-name>track" in s.first_prompt
                                                   or "<command-name>/track" in s.first_prompt)]
    tc = sum(s.card_calls for s in tr); tout = sum(s.tot()["out"] for s in tr)
    test = sum(s.card_out_est for s in tr); tmsg = sum(s.card_msg_out for s in tr)
    treq = sum(s.tot()["req"] for s in tr); tcost = sum(s.tot()["cost"] for s in tr)
    P(f"track sessions: {len(tr)}, requests {treq}, $ {tcost:.0f}, card Write/Edit/mv calls {tc} "
      f"({tc/max(treq,1):.2f}/request, {tc/max(len(tr),1):.1f}/session)")
    P(f"output tokens: {fmt(tout)}; card tool-input est {fmt(test)} ({test/max(tout,1):.1%}); "
      f"output of messages containing a card call {fmt(tmsg)} ({tmsg/max(tout,1):.1%}, upper bound)")
    kinds = Counter()
    for s in tr:
        kinds.update(getattr(s, "card_kinds", Counter()))
    P("card calls by tool: " + ", ".join(f"{k} {v}" for k, v in kinds.most_common()))
    allcard = sum(s.card_calls for s in allsess)
    P(f"card calls in all sessions (incl. non-/track-started): {allcard}")

    # 6. top sessions
    P("\n## 6. Top 10 sessions by $ (incl. subagents)")
    def full(s):
        t = s.tot()
        for sa in s.subagents:
            t.update(sa.tot())
        return t
    top = sorted(allsess, key=lambda s: -full(s)["cost"])[:10]
    rows = []
    for s in top:
        t = full(s)
        cwd = (s.cwd or "?").replace(os.path.expanduser("~"), "~")
        fp = re.sub(r"\s+", " ", (s.first_prompt or ""))[:100]
        rows.append([s.sid[:8], f"{t['cost']:.0f}", fmt(t["out"]), sum(1 for x in s.subagents if x.reqs), cwd[-30:], fp])
    P(table(rows, ["session", "$", "out", "subs", "cwd", "first prompt"]))

    # 7. tool mix
    P("\n## 7. Tool-call mix (main + subagents)")
    tm = Counter(); tmm = Counter(); tms = Counter()
    for s in allsess:
        tm.update(s.tools); tmm.update(s.tools)
        for sa in s.subagents:
            tm.update(sa.tools); tms.update(sa.tools)
    tot = sum(tm.values())
    mcp = sum(v for k, v in tm.items() if k.startswith("mcp__"))
    P(f"total tool_use {tot}; MCP {mcp} ({mcp/max(tot,1):.1%}); ToolSearch {tm['ToolSearch']} ({tm['ToolSearch']/max(tot,1):.1%})")
    P(table([[k, v, f"{v/tot:.1%}", tmm[k], tms[k]] for k, v in tm.most_common(20)], ["tool", "n", "share", "main", "sub"]))
    mcps = Counter()
    for k, v in tm.items():
        if k.startswith("mcp__"):
            mcps[k.split("__")[1]] += v
    P("MCP by server: " + ", ".join(f"{k} {v}" for k, v in mcps.most_common(10)))

    print("\n".join(out))
    if a.json:
        json.dump({"since": str(since), "until": str(until), "total": agg[("all",)], "main": agg[("main",)], "sub": agg[("sub",)],
                   "fixed_overhead_median": statistics.median(allfo) if allfo else None,
                   "track_card_calls": tc, "tools": tm}, open(a.json, "w"), indent=1, default=str)


if __name__ == "__main__":
    main()
