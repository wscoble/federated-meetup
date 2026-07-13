# 09 — Onboarding & Import

**Scope:** The first 5 minutes of an organizer's relationship with Federated Meetup. The two paths in: (A) start fresh, (B) import an existing meetup.com group. The settings surface they land in after.

**Why this doc exists:** v0.4 calls for "Onboarding automation" and "Migration day … LLM with platform-specific schema handles most imports" but neither has a concrete spec. The four issues (#10, #11, #12, #13) implement this doc.

**In scope:**
- `/groups/new` redesign — import path + AI-assisted fresh-start
- `/groups/<slug>/import` flow — URL paste, CSV upload, review
- `/groups/<slug>/settings` — tabs for Profile, Members, Hosting, Federation, Danger Zone
- Onboarding success state — "What's next?" with AI-generated first-event suggestions

**Out of scope:** Discovery / search / browse (separate). Payment (Package C) (separate). Federation relay (v1). Cross-host event search (v1).

---

## 1. The first-touch contract

The home page is currently the only entry point. Three jobs for the first-touch surface:

1. **Help a curious visitor** understand what this is — "federated events for organizers, not another social network"
2. **Help a frustrated meetup.com organizer** get out — "paste your URL, get a near-complete group in 60 seconds"
3. **Help a new organizer** start clean — "create a group, draft a description with AI, ship your first event in 5 minutes"

The current home page does none of these well. The "Be the first to host something" empty state is a fail for the first 99% of visitors. We'll fix that in this doc.

### Updated home page (description, not PR)

- Hero: "Federated events, owned by organizers." Subhead: "No algorithm. No app. No promoted events. Just your group, your data, your federation."
- Two CTAs side by side:
  - Primary: **"Create a group"** (→ `/groups/new` redesigned, see §2)
  - Secondary: **"Import from meetup.com"** (→ `/groups/import` URL flow, see §3)
- Below the fold: live group count, live event count, "What's federation?" link, "Why we're better than meetup.com" link (the latter is a `01-PROBLEM.md`-derived longform; defer)

---

## 2. `/groups/new` — the fresh-start path

### Current state

`templates/new_group.html` is a thin form: 4 fields (display name, description, organizer name, organizer email), no AI, no event-type guidance, no joy. Catalog line 54: production, no annotations, basic.

### Target state

- **Top section: import shortcut.** Single-line: "Already have a group on meetup.com? [Import it →](/groups/import)"
- **Event type picker (radio cards).** Four cards, one selected (default: A — Free Community):
  - **A: Free Community** — basic events, RSVP, no tickets
  - **B: Recurring Club** — everything in A, plus recurring events and member roles
  - **C: Ticketed Workshop** — everything in B, plus Stripe Connect ticketing
  - **D: Conference** — everything in C, plus multi-track, sponsor tiers (v1)
  - v0 ships A and C; B and D shown but disabled with a "Coming soon" badge. v0.4 §12 says v0 ships C only, but A is already production per the catalog — this aligns shipped state with the picker.
- **Group name + description fields.** Description gets a small "✨ Draft with AI" button next to the label (D8 — inline, not a chat surface).
- **AI draft modal (HTMX swap).** Click "Draft with AI" → 4 questions appear inline:
  - "What kind of event or group is this?" (free text, 1 sentence)
  - "Who's the audience?" (pick from 8 archetypes)
  - "What's the vibe?" (pick from 5)
  - "Anything specific you want members to know?" (optional free text)
  - "Generate draft" button. Draft appears in an editable textarea below. Organizer edits, accepts, or rejects. **D9: AI output is a draft, organizer is the author.** D10: per-group "Use AI features" toggle; disabled toggle hides all AI affordances.
- **Organizer info section.** Name, email, "How we'll use this" footnote ("Your email is the login for the organizer dashboard. Members never see it.")
- **Submit.** Group created → redirect to `/groups/<slug>/dashboard?welcome=1` (see §5).

### AI parity (D10 + v0.4 §3)

- Per-group toggle: "Use AI features" (default: on)
- Per-event opt-out (sticky per event type)
- Self-hosters: configure their own model endpoint (Ollama, vLLM, or OpenAI-compatible); with no AI configured, the form still works — no 500s, just no AI affordance

### Acceptance

- Form is mobile-first, all fields touch-friendly
- AI draft returns in <5 sec; disabled state shows "AI features disabled" tooltip
- Submit creates group + organizer session in <1 sec (existing handler stays, just gets more inputs)
- AI suggestions logged to `ai_suggestions` table per v0.4 §10

---

## 3. `/groups/import` — the meetup.com import path

This is the v0 hero feature. Two sub-flows: URL import (P0, no PII) and CSV import (P1, consent-gated PII).

### 3.1 URL import (issue #10)

**The principle:** meetup.com group pages are public. We scrape them. No PII crosses the wire. Members re-find the group organically and re-RSVP. This sidesteps the GDPR/TOS risk entirely.

**Flow:**

1. `/groups/import` shows a single input: "Paste your meetup.com group URL" + an explanation card ("We pull the public page — group name, description, past + upcoming events. We never see your member list. You review everything before it's saved.")
2. Organizer submits URL → backend fetches `https://www.meetup.com/<slug>/` with a realistic User-Agent, parses HTML with `golang.org/x/net/html`, returns structured data
3. Progress UI: "Fetching your group…", "Found 42 events, importing…", "Ready for review" (HTMX-driven, no full page reloads)
4. `/groups/import/review` — shows what we found in editable form fields: group name, description, location, organizer display name, list of imported events (with checkbox to skip individual events). Organizer edits, then clicks "Create Group"
5. Group created → redirect to `/groups/<slug>/dashboard?welcome=1&imported=1`

**Backend: `internal/import/meetup.go`**

- `FetchGroup(slug) (*meetupGroup, error)` — HTTP GET, 10s timeout, 1 retry on 5xx
- Parses: `h1` (group name), `About` section (description), location metadata, organizer names, event titles + dates + locations + descriptions
- Cache: 24h in-memory keyed by slug
- Rate limit: 10 imports per IP per hour
- Honors `robots.txt`
- Fixtures: `test/fixtures/meetup/group-page-2026-07-13.html` and similar, pinned to a snapshot date so tests don't break on redesign

**Schema:**

- `import_sessions` (in-memory or short-lived SQLite): stores fetched data keyed by short token, expires 30 min
- `groups.import_source` (e.g., `meetup.com/vegas-programmers`) — attribution
- `events.import_source_event_id` — de-duplication on re-import

**Anti-dox: the URL import must never carry PII.** Tests pin this. Backend response shape is whitelisted — no member fields exist in `meetupGroup` struct.

**Mobile-first:** the entire flow is a single column on mobile. Big input, big button, big progress.

### 3.2 CSV import (issue #11) — v0.5 / v1, not v0

URL import is the v0 path. CSV import is the "complete migration" path. It's a separate issue (#11) with its own threat model, scope, and review flow.

**The principle:** member PII belongs to members, not organizers. Re-uploading a meetup.com member list to a competing service without per-member consent is a GDPR violation and an anti-dox failure. The flow enforces consent at three gates — see issue #11 for the full spec.

**v0 ships URL import only.** CSV import lands in v0.5/v1, behind the legal review of the opt-in email copy and consent gate language.

### 3.3 What we don't import (deferred / never)

- Bidirectional sync with meetup.com (TOS risk, never)
- Group discussion board posts (not exportable in standard CSV)
- Past RSVP history (not public; only available via organizer's own CSV export, which the consent flow handles)

---

## 4. `/groups/<slug>/settings` — the destination (issue #13)

### Tabs

**Profile** — display name, description, location, logo (image upload, max 2 MB, PNG/JPG/WebP), cover image (max 5 MB), categories (multi-select from a fixed v0 taxonomy), group URL/handle, custom domain (Pro+ tier only)

**Hosting** — read-only hosting mode (SELF_HOST or PAID_HOST, set at deploy), hosting tier picker (Free / Community / Pro / Studio), current usage, upgrade/downgrade flow (links to billing — out of scope for v0)

**Members** — list with role badges, invite form (email), role change dropdown per member. Re-uses the consent flow from CSV import.

**Import** — re-import from meetup.com (if URL changed), CSV import link (when v0.5 lands), export data (JSON)

**Federation** — ActivityPub actor URL, follower count, federation policy (allowlist/denylist/blocked instances), connection status

**Danger Zone** — transfer ownership (2-of-3 owner quorum per v0.4 §7 v2 spec), delete group (requires typing group name), export all data (JSON dump)

### Auth

- Settings page requires organizer token (existing) OR member role-based access (new)
- OWNER + ORGANIZER can edit; others see read-only
- Cross-group IDOR fix (existing C-3) must apply to all new handlers

### Audit log

- Every settings change writes an entry to the audit log (links to existing C-4 token-fingerprint pattern)
- v0 ships read-only audit log (in admin); queryable UI is v1

---

## 5. Onboarding success state — `?welcome=1`

After group creation (fresh or import), redirect to `/groups/<slug>/dashboard?welcome=1`. The dashboard hero becomes a "Let's get your first event live" card with three AI-generated event suggestions based on group description (titles + one-line summaries).

**Per "Schedule" button:** opens `/groups/<slug>/events/new?title=<suggested>` with title pre-filled. Description and schedule fields both get AI assist buttons. Conflict detection (v0.4 §6) flags RSVP scheduling conflicts before publish.

**Per "Skip" link:** opens `/groups/<slug>/events/new` blank.

**The "welcome=1" banner** dismisses on click and never returns (cookie). It does not block the dashboard; the dashboard is fully usable without dismissing.

---

## 6. Joy metric instrumentation

v0.4 §1 names the metrics. Add client-side + backend instrumentation to measure:

- **Organizer time-to-event-created** — from first `/groups/new` GET to first event-create POST. Target: <5 min
- **URL import completion rate** — of organizers who land on `/groups/import`, what % complete the review + create flow? Target: >70%
- **AI draft accept rate** — of AI-generated drafts shown, what % are accepted (unedited) vs edited vs rejected? Target: >40% accepted or edited
- **First-event publish rate within 24h of group create** — the most direct measure of "joy" working. Target: >50%

All metrics logged via existing Prometheus endpoint; no PII in metric labels (counts only).

---

## 7. Anti-dox posture (cross-cutting)

The platform's reputation is built on being safe with attendance data. Every flow in this doc must be reviewed against the seven checks in `~/.hermes/skills/security/anti-dox/SKILL.md`. Specific commitments:

- **URL import:** no PII in backend response shape; no member fields in `meetupGroup` struct; no logs of fetched content beyond HTTP status
- **CSV import:** email addresses never stored plaintext, only `email_hash`; opt-in tokens single-use with TTL; rate-limited confirmation page (anti-enumeration)
- **Settings page:** member list visible only to OWNER/ORGANIZER; email addresses never displayed in plain (last-4 redaction pattern, like Stripe)
- **AI prompts:** never include member emails, never include RSVP lists, never include per-attendee PII. Group description and event titles only
- **Welcome banner:** no third-party trackers; uses first-party cookie only
- **Dox-test:** every new endpoint has a test that confirms third-party cannot enumerate member data

**Open issue #9 (`/my-rsvps` dox) is the highest-priority open bug in the project. It blocks any claim of "production-safe for members."** The new import flows are designed to be safe by construction even before #9 is fixed, but the platform cannot claim v0 launch until #9 is closed.

---

## 8. Acceptance criteria (cross-cutting)

For v0 launch readiness, this doc's full scope requires:

- [ ] All 4 issues (#10, #11, #12, #13) implemented and accepted
- [ ] Joy metric instrumentation live
- [ ] Anti-dox test suite passing for all new endpoints
- [ ] AI parity: with no AI configured, the full flow works
- [ ] Mobile-first: the entire flow usable on a phone in <90 sec
- [ ] Issue #9 closed and regression-tested
- [ ] v0.4 strategic doc reconciled (#14) and matches shipped state

For v0.5 / v1 expansion:

- [ ] CSV import ships with legal review of opt-in email copy
- [ ] Member join/leave public flow ships
- [ ] Public member directory ships (privacy-mode opt-in per member)
- [ ] Cross-host import (other federated-meetup instances) ships

---

## 9. Open questions

1. **Should the URL import flow require a logged-in organizer session, or can anonymous visitors "stage" a group and claim it via email later?** Defer to implementation — staging is fine for v0, claiming is email-based.

2. **Should the meetup.com URL import page be discoverable from the public home page, or only behind a "Create a group" → "Already have one?" branch?** Recommend the latter — keeps the home page clean, the import path is the alternative to the fresh-start path, not a parallel one.

3. **Categories taxonomy: v0 ships 17 fixed categories. Should we crowdsource additions?** No — crowdsourced taxonomy is moderation work; defer to v1.

4. **Should the welcome banner be AI-generated or hand-crafted?** AI-generated per group, but with a hand-crafted fallback template. The fallback is what gets rendered in tests.

5. **Should we import past events as "past" or hide them by default?** Past events appear on the group page in a separate "Past events" section (matches existing 7/3 UX redesign). Imported past events inherit this — visible but not in the upcoming list.

6. **What if the meetup.com page is down or 404s?** Surface a clear error: "We couldn't fetch that group. Check the URL or try again later." Don't fall through to a half-imported group.

7. **What if the meetup.com page returns a CAPTCHA challenge?** Surface a clear error: "Meetup.com is asking us to verify we're human. Try again in a few minutes." Don't try to solve the CAPTCHA.

8. **What if two organizers try to import the same group URL at the same time?** Cache layer serializes; the second organizer sees the cached result, no double-fetch.

---

## Related docs and issues

- [PRIVACY.md](../PRIVACY.md) — anti-dox commitments, data handling, user rights (the anti-dox posture in §7 below is mirrored in PRIVACY.md §2 with testable clauses)
- [TERMS.md](../TERMS.md) — user agreement, organizer duties, self-host terms
- [SECURITY.md](../SECURITY.md) — auditor-facing security policy and infrastructure hardening
- v0.4 strategic frame: `~/.hermes/scratch/federated-meetup-design-v0.4.md` (§1 joy, §3 AI parity, §4 pricing tiers, §5 D8/D9/D10, §6 AI operating principle, §7 federation, §12 v0 scope)
- v0.4 reconciliation: issue #14 (drift in Postgres vs SQLite, recurring events, package A/C)
- Audit: `AUDIT-2026-07-06.md` (C-3 cross-group IDOR, C-4 token logging, H-4 SMTP CRLF, M-5 POST body cap — all apply to new flows)
- Anti-dox skill: `~/.hermes/skills/security/anti-dox/SKILL.md` (seven checks)
- Open dox issue: #9
- Issue #10: URL import (P0)
- Issue #11: CSV import (P1, v0.5)
- Issue #12: Onboarding first-run (P0)
- Issue #13: Group settings page (P0)
- Issue #14: v0.4 doc reconciliation (P2)
