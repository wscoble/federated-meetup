# Federated Meetup — Privacy Policy

**Version:** v0.1 (2026-07-13)
**Applies to:** Federated Meetup instances operated by Greybeard Holdings LLC ("Greybeard", "we", "us"). If you are using a self-hosted instance operated by someone else, that operator's privacy policy applies to your data on their instance — not this one.
**TL;DR:** We don't sell your data. We don't log your attendance. We don't let organizers bulk-export your email. We don't run ads. We don't train AI models on your activity. If this policy is silent on something that affects you, we resolve it in your favor.

**See also:** [TERMS.md](./TERMS.md) for the user agreement, organizer duties, and self-host terms. [SECURITY.md](./SECURITY.md) for vulnerability reporting and infrastructure hardening.

---

## 0. The "user wins" principle (this is the most important section)

```
Scope UserVsOperator:
  context question: Question
  context policy_text: PolicyText

  If question is not_addressed_by policy_text:
    Then resolution is "interpret-in-favor-of-user"
```

**In plain English:** Where this policy is silent or ambiguous on a question that affects you as a user (attendee, member, organizer, self-host operator), we will interpret it in your favor. This is not a marketing line. It is a binding commitment: when in doubt, you win.

---

## 1. What we mean by "user"

There are three kinds of users on Federated Meetup, and this policy treats each one as a user with rights:

- **Attendee** — someone who RSVPs to an event. The protected party in this policy.
- **Member** — someone who has joined a group.
- **Organizer** — someone who runs a group or hosts events. Has more responsibility, not more power over attendees.

**Self-host operators** are also users of our software. When you self-host, *you* are the data controller for *your* users' data. This policy is a template you can adopt; we don't claim authority over your instance.

```
Scope UserClassification:
  context person: Person

  If person has_role "attendee" or "member" or "organizer":
    Then person.is_user is true
    And person.is_protected_party depends on person.role
    (See anti-dox scope: attendees and members are protected; organizers have more responsibility.)
```

---

## 2. Anti-dox (the load-bearing commitment)

This is the part of the policy that maps to actual code. Every clause below corresponds to a test in our open-source regression suite. If we break one of these promises, the test fails and the build fails.

```
Scope AntiDox:

  context request: Request
  context requester: Person

  ## Attendee enumeration prevention
  If request is "list-rsvps" and requester does_not_have valid_magic_link_token:
    Then response is denied

  If request is "list-rsvps" and requester credential is "email-only":
    Then response is denied
    (Issue #9: email is NOT a credential. Magic-link token IS the credential.)

  If request is "cancel-rsvp" and requester does_not_have valid_rsvp_token:
    Then response is denied

  ## Member PII protection
  If request is "bulk-export-member-emails":
    Then response is denied-even-to-organizer
    (Organizers may invite members one at a time via opt-in flow. No bulk export.)

  If request is "list-members-by-name" and requester is "public-visitor":
    Then response is "redacted-to-count-only"

  ## Token handling
  If request involves organizer_token:
    Then log entry is fingerprint_only
    (Audit log correlation via SHA-256, not raw token. C-4 in audit log.)

  If request involves rsvp_token:
    Then token is single_use
    And token has ttl of 24 hours
    And token usage is not logged with email
```

**In plain English:**

- We will not let anyone read your RSVP history without your magic-link token. Email alone is not a credential.
- We will not let anyone cancel your RSVP without your single-use token.
- We will not let an organizer bulk-export their member list. They can invite members one at a time, with each member's consent.
- Public visitors see member *count*, not member *names*.
- Organizer tokens never appear in logs. RSVP tokens are single-use, expire in 24 hours, and are never logged alongside your email.
- The code that implements these commitments is in `internal/web/anti_dox_test.go` and is public.

---

## 3. What data we collect, and why

```
Scope DataCollection:
  context data_kind: DataKind
  context purpose: Purpose

  ## Data we collect
  If data_kind is "rsvp-name" and data_kind is "rsvp-email":
    Then purpose is "deliver-event-confirmation-and-reminder"
    And retention is "until-event-plus-30-days-then-removed-from-active-views"
    And on_event_cancellation: marked_cancelled_and_retained_in_signed_log; user-visible-copy-removed-from-active-views-after-30-days
    # Note (2026-07-19): the protocol's state log is append-only and replicated
    # across mirrors (02-PROTOCOL.md §3.1, §5.0, §9); "deletion" here means removal
    # from active UI and downstream exports, not erasure from the signed transition
    # history. See §"Right to deletion" below for the full scope.

  If data_kind is "organizer-name" and data_kind is "organizer-email":
    Then purpose is "organizer-authentication-and-dashboard-access"
    And retention is "while-group-active-plus-90-days-after-deletion"

  If data_kind is "event-attendance-record":
    Then purpose is "event-check-in-and-organizer-reporting"
    And visible_to is "event-organizer-only"
    And retention is "while-event-exists"

  If data_kind is "audit-log-entry":
    Then purpose is "security-incident-response"
    And retention is "90-days-then-aggregated-counts-only"
    And contains_pii is false
    (Token fingerprints, not tokens. Email hashes, not emails. IP addresses, not user identities.)

  ## Data we do NOT collect
  If data_kind is "browsing-history" or "cross-site-tracking":
    Then we_do_not_collect

  If data_kind is "advertising-profile":
    Then we_do_not_collect

  If data_kind is "ai-training-data":
    Then we_do_not_collect
    (We do not train AI models on your activity. AI suggestions are generated per-request and not retained beyond the suggestion flow.)
```

**In plain English:**

| What | Why | How long |
|---|---|---|
| Your name and email when you RSVP | To send you the event confirmation and a reminder | Until the event, plus 30 days, then deleted |
| Event organizer name and email | To log them in to the dashboard | While the group exists, plus 90 days |
| Whether you attended an event | To let the organizer check you in and report attendance | While the event exists |
| Audit log entries (token fingerprints, email hashes, IPs) | To investigate security incidents | 90 days, then aggregated counts only |

**We do not collect:** browsing history across other sites, advertising profiles, biometric data, location beyond what you provide, AI training data.

---

## 4. Who we share data with

```
Scope DataSharing:
  context data_kind: DataKind
  context recipient: Recipient

  If recipient is "event-organizer" and data_kind is "rsvp":
    Then sharing is permitted
    And scope is "this-event-only"
    And organizer is bound by their own privacy obligations

  If recipient is "federated-instance" and data_kind is "event-metadata":
    Then sharing is permitted-if-user-consented
    (Federation note: RSVPs are signed transitions and are replicated to mirrors and peer hosts serving the group per 02-PROTOCOL.md §3.1, §5.0, §9. The RSVP payload contains the user's identity and event reference; it does not contain email or other contact PII by default. Self-hosters and mirror operators see the same RSVP data the primary host sees.)

  If recipient is "payment-processor" and data_kind is "purchase-info":
    Then sharing is to "stripe" only
    And scope is "payment-processing-only"
    And we do not see your card number (Stripe handles it)

  If recipient is "email-provider" and data_kind is "email-content":
    Then sharing is to delivery_provider only
    And content is "content-free-where-possible"
    (Magic-link emails do not include event names in subject lines; minimal information disclosure.)

  If recipient is "advertiser" or "data-broker" or "third-party-analytics":
    Then sharing is never
```

**In plain English:**

- **The event organizer** sees your RSVP. That's the deal — they need to know who's coming. They see it for the events they organize, not for any other group.
- **Other federated instances** see event metadata (title, time, location) if you publish the event publicly. RSVPs are not federated in v0.
- **Stripe** sees your payment info. We never see your card number.
- **Our email provider** sees the email content. We keep it minimal — no subject line event names, no tracking pixels.
- **Advertisers, data brokers, and third-party analytics: never.** We don't have any of these relationships.

---

## 5. Your rights (GDPR, CCPA, and you-have-rights-anyway)

```
Scope UserRights:
  context request: Request
  context requester: Person

  ## Right to access
  If request is "export-my-data":
    Then response is json_dump of all_data_held_about requester
    And delivered within 30 days
    And delivered to verified_email of requester only

  ## Right to deletion
  If request is "delete-my-data":
    Then response is remove-from-member-lists-and-active-views
    And completed within 30 days
    And exception is "data-required-for-active-event" (held until event concludes)
    And exception is "data-required-for-legal-obligation" (held per law)
    And note: "the signed transition history (append-only, replicated across mirrors per 02-PROTOCOL.md §3.1 and §9) is retained per protocol; user-facing data is purged within 30 days from Greybeard-operated hosts and mirrors under our control. Data on third-party hosts/mirrors is subject to that operator's policy."

  ## Right to correction
  If request is "correct-my-data":
    Then response is update of specified_field
    And requires verification that requester is the data_subject

  ## Right to portability
  If request is "export-for-migration":
    Then format is json
    And includes all user-visible data
    And includes import_token for verification

  ## Right to be informed of breach
  If breach affects requester and breach is confirmed:
    Then notification is within 72 hours
    And includes what_was_affected
    And includes what_we_are_doing

  ## Right to complain
  If request is "escalate-privacy-concern":
    Then response is direct_contact to scott@scoble.me
    And acknowledged within 48 hours
    And resolved in user's favor where policy is ambiguous (see §0)
```

**In plain English — you can:**

- **Export your data** — get a JSON file of everything we hold about you, within 30 days
- **Delete your data** — we delete it within 30 days, with narrow exceptions for active events and legal obligations
- **Correct your data** — fix anything that's wrong
- **Move your data** — export it in a format that imports cleanly into another platform
- **Know about breaches** — we'll tell you within 72 hours if your data was affected
- **Escalate to a human** — email scott@scoble.me, response within 48 hours

You don't need to cite GDPR or CCPA to exercise these. They're your rights as a user, not as a legal entity.

---

## 6. Cookies and tracking

```
Scope Cookies:
  context cookie: Cookie

  If cookie is "session":
    Then purpose is "authentication"
    And lifetime is "browser-session"
    And tracking is false

  If cookie is "csrf-token":
    Then purpose is "csrf-protection"
    And lifetime is "browser-session"
    And tracking is false

  If cookie is "theme-preference":
    Then purpose is "user-experience"
    And lifetime is "1-year"
    And tracking is false

  If cookie is "analytics" or "advertising" or "third-party-tracker":
    Then we_do_not_set
```

**In plain English:** Three cookies: one to keep you logged in, one to prevent CSRF attacks, one to remember if you like dark mode. That's it. No analytics cookies. No third-party trackers. No advertising.

---

## 7. International transfers and federation

Federated Meetup instances can be self-hosted anywhere. If you use an instance operated by Greybeard, your data is stored in the region that instance operates in. Note (2026-07-19): per 02-PROTOCOL.md §9, group state may be replicated to mirrors operated by third parties, whose residency is outside Greybeard's control. Stewards may restrict mirror peers per group policy; consult your group's steward set for the current mirror list.

```
Scope DataResidency:
  context instance: Instance
  context data_kind: DataKind

  If instance is "greybeard-operated":
    Then residency is disclosed in instance documentation
    And standard_contractual_clauses apply for cross-border transfers

  If instance is "self-hosted":
    Then residency is operator's responsibility
    And this policy is a template
```

**In plain English:** If you're on a Greybeard-operated instance, your data is in [region]. We use standard contractual clauses for cross-border transfers where required. If you're on a self-hosted instance, the operator's data residency rules apply — read their privacy policy.

---

## 8. AI features (v0.4 §6)

We use AI for description drafting, schedule suggestions, and email drafting. Here's the commitment:

```
Scope AI:
  context data_kind: DataKind
  context ai_use: AIUse

  If ai_use is "description-drafting" or "schedule-suggestion" or "email-drafting":
    Then data_sent_to_model is "group-description-and-event-context-only"
    And data_not_sent is "member-emails, rsvp-lists, attendee-names"
    And model_response is "draft-not-decision" (organizer approves before use)
    And ai_suggestions are logged (accept/reject) for quality tracking
    And ai_suggestions_do_not_include member PII

  If ai_use is "training":
    Then we_do_not_train
    And we do not use your content as training data
```

**In plain English:** AI suggestions are based on your group description and event context. They never see your member list, your attendees' emails, or anyone's RSVP data. AI outputs are drafts — you approve before they go live. We don't train AI models on your content.

---

## 9. Children

Federated Meetup is not directed at children under 13. We do not knowingly collect data from children under 13. If we learn we have, we delete it.

---

## 10. Changes to this policy

```
Scope PolicyChanges:
  context change: PolicyChange

  If change is "substantive":
    Then notice is given 30 days in advance
    And notice is to active_organizers and active_members
    And prior version is archived

  If change is "editorial-typo" or "clarification":
    Then notice is given at next login
    And no advance notice required
```

**In plain English:** If we change this in a way that affects your rights, we give you 30 days notice by email. If it's a typo, we just fix it.

---

## 11. Contact

For privacy questions, data requests, or to report a privacy concern:

- **Email:** scott@scoble.me
- **Response time:** 48 hours
- **Escalation:** Scott Scoble, Greybeard Holdings LLC

---

## Appendix A: Test mapping (for the curious)

Every scope clause above maps to a test. If you want to verify our commitments hold:

| Policy section | Test location |
|---|---|
| §2 anti-dox | `internal/web/anti_dox_test.go` |
| §2 token handling | `internal/web/cookies_hardening_test.go` |
| §3 retention | `internal/store/retention_test.go` (TBD) |
| §4 organizer data scope | `internal/web/authz_hardening_test.go` |
| §5 data export | `internal/web/data_export_test.go` (TBD) |
| §6 cookies | `internal/web/server.go` cookie definitions |
| §8 AI data scope | `internal/ai/draft.go` prompt construction tests (TBD) |

Tests marked TBD are part of v0.4 §12 scope and not yet written. The privacy policy is a contract — the tests are how we know we're keeping it.

---

## Appendix B: Self-hosters

If you operate a self-hosted instance:

- **You are the data controller** for your users' data, not us
- This policy is a **template** you can adopt, modify, or replace
- We recommend you write your own privacy policy that names your legal entity, contact, and data residency
- We do not collect data from your instance unless you opt in to telemetry (v0 ships with telemetry off by default)
- If you adopt this policy verbatim, please credit Greybeard and link back; if you modify it materially, please drop the Greybeard attribution

---

*This policy is written in the Catala idiom — conditions before conclusions, deterministic outcomes, every commitment maps to a test. Real Catala compilation requires legal expertise we don't claim to have. A lawyer should review this before relying on it in a dispute. The Catala-style structure is here so that, when we do have legal review, the translation to enforceable policy is straightforward.*
