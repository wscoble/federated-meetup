# Federated Meetup — Terms of Service

**Version:** v0.1 (2026-07-13)
**Applies to:** Federated Meetup instances operated by Greybeard Holdings LLC ("Greybeard", "we", "us"). If you are using a self-hosted instance operated by someone else, that operator's terms apply to your use of their instance — not this one.
**TL;DR:** Use the product. Don't break it. Don't hurt other users. When there's a dispute, you win. We're not liable for things outside our control. Self-hosters run their own show.

**See also:** [PRIVACY.md](./PRIVACY.md) for data handling, anti-dox commitments, and user rights. [SECURITY.md](./SECURITY.md) for vulnerability reporting and infrastructure hardening.

---

## 0. The "user wins" principle (this is the most important section)

```
Scope UserVsOperator:
  context question: Question
  context terms_text: TermsText

  If question is not_addressed_by terms_text:
    Then resolution is "interpret-in-favor-of-user"

  If question is "ambiguous-in-our-favor":
    Then resolution is "interpret-in-favor-of-user"

  If question is "ambiguous-in-your-favor":
    Then resolution is "interpret-in-favor-of-user"
```

**In plain English:** Where these terms are silent or ambiguous, we interpret them in your favor. This is not a marketing line. It is a binding commitment: when in doubt, you win.

---

## 1. Who you are in this agreement

There are three kinds of users, and your relationship with us depends on which:

- **Attendee / Member** — you RSVP to events, join groups. You agree to these terms by using the service. The terms primarily protect your privacy and your access.
- **Organizer** — you run a group. You agree to additional responsibilities (you are also a data controller for your group's data; you agree not to misuse the service). The terms grant you tools and obligations.
- **Self-host operator** — you run your own instance. **These terms do not bind you to us.** You have your own relationship with your users under your own terms. We provide the software; you provide the service.

```
Scope UserRole:
  context person: Person

  If person has_role "attendee":
    Then agreement scope is "use-of-service-as-attendee"

  If person has_role "organizer":
    Then agreement scope is "use-of-service-as-organizer"
    And additional obligations apply (see §4)

  If person has_role "self-host-operator":
    Then agreement scope is "license-to-software-only"
    And user-to-operator-relationship is with their own users under their own terms
```

---

## 2. Your account and access

```
Scope AccountAccess:
  context action: Action
  context person: Person

  ## Authentication
  If action is "access-organizer-dashboard":
    Then credential required is "organizer-token"
    And token has 7-day TTL
    And token fingerprint is logged (not token) for audit

  If action is "access-my-rsvps":
    Then credential required is "magic-link-session-token"
    And token is single-use
    And token has 24-hour TTL
    And rate-limit is 0.2 req/sec per IP (anti-enumeration)

  ## Account recovery
  If action is "recover-organizer-access":
    Then flow is "email-magic-link-to-organizer-email"
    And no password reset (we don't store passwords)

  ## Account termination
  If action is "delete-account":
    Then flow is "request-via-email"
    And completed within 30 days
    And exception is "data-required-for-active-event" (held until event concludes)
```

**In plain English:**

- **Organizers** log in with a token, not a password. We can't reset your password because we never had it.
- **Attendees** use a magic-link session token. Single-use, 24 hours.
- **You can delete your account anytime.** We delete your data within 30 days, with narrow exceptions for active events.

---

## 3. What you can do (and what you can't)

```
Scope AcceptableUse:
  context action: Action
  context person: Person

  ## Permitted
  If person has_role "attendee":
    Then person can: rsvp-to-events, cancel-rsvp, list-own-rsvps, receive-event-reminders

  If person has_role "organizer":
    Then person can additionally: create-group, create-event, manage-own-group, configure-own-group

  ## Prohibited
  If action is "scrape-event-attendees-for-marketing":
    Then action is prohibited
    (Anti-dox: no bulk export of member data, ever.)

  If action is "send-unsolicited-bulk-email-via-our-service":
    Then action is prohibited
    (No spam. Our email infrastructure is for event comms, not marketing.)

  If action is "impersonate-another-organizer":
    Then action is prohibited

  If action is "use-service-to-harass-attendees":
    Then action is prohibited
    And account is subject to suspension per §6

  If action is "reverse-engineer-our-paid-services-to-avoid-payment":
    Then action is prohibited
    (But: self-hosting is permitted. The product is free-as-in-beer if you operate it yourself.)

  ## Always permitted
  If action is "self-host-the-software":
    Then action is permitted under AGPL-3.0
    (The protocol and the self-hostable product are open source. You can run your own instance. We want you to.)

  If action is "federate-with-other-instances":
    Then action is permitted
    (Federation is the point. ActivityPub is enabled by default for v0.)
```

**In plain English — you can:**

- RSVP to events
- Run a group (if you're an organizer)
- Self-host the software (it's open source, AGPL-3.0)
- Federate with other instances

**You can't:**

- Bulk-export member data (the platform doesn't even let you try)
- Send spam through our email infrastructure
- Impersonate other organizers
- Harass attendees
- Reverse-engineer paid services to avoid payment — but you don't need to, because self-hosting gives you everything for free

---

## 4. Organizer responsibilities

If you run a group, you have additional obligations to your attendees:

```
Scope OrganizerDuties:
  context action: Action
  context organizer: Person

  If organizer has_role "organizer":
    Then organizer is data_controller for group's user data
    And organizer must: protect-attendee-privacy, honor-RSVP-cancellations, provide-accurate-event-info
    And organizer must_not: bulk-export-member-emails, share-attendee-data-without-consent, send-spam-via-our-service
    And organizer must: respect magic-link-token TTLs (don't try to circumvent single-use)

  If organizer violates attendee privacy:
    Then account is subject to suspension per §6
    And affected attendees are notified
```

**In plain English — if you organize events:**

- You are the data controller for your group's data
- You must protect attendee privacy
- You must honor RSVP cancellations
- You must provide accurate event information
- You must not bulk-export your member list (we don't let you anyway, but you must not try to circumvent)
- You must not share attendee data with third parties without consent
- You must not send spam through our email infrastructure

If you violate attendee privacy, we suspend the account and notify the affected attendees.

---

## 5. Our commitments to you

```
Scope OurCommitments:
  context commitment: Commitment
  context user: Person

  ## Availability
  If commitment is "uptime":
    Then target is "best-effort-99-percent"
    And status is published at status.fm.scoble.me
    And SLA credit is "none-promised-self-host-elsewhere-if-needed"
    (We don't promise five-nines. We promise "the lights stay on." If you need SLA, paid-host tier offers it.)

  ## Data handling
  If commitment is "data-protection":
    Then we will not: sell-your-data, train-AI-on-your-data, share-with-advertisers, share-with-data-brokers
    And we will: honor data export and deletion requests per PRIVACY.md §5, notify you of breaches within 72 hours, run anti-dox regression tests in CI

  ## Open source
  If commitment is "self-host-parity":
    Then every feature in paid-host ships in self-host
    And the protocol is AGPL-3.0
    And we publish the anti-dox regression test suite

  ## Support
  If commitment is "support":
    Then response is within 48 hours for security issues
    And response is best-effort for general questions
    And self-hosters have community support (paid-host customers get prioritized response)
```

**In plain English — we promise to:**

- Keep the service running (99% target, not five-nines — if you need SLA, paid-host)
- Never sell your data, train AI on your data, or share with advertisers
- Honor data export and deletion requests per our privacy policy
- Notify you of breaches within 72 hours
- Run anti-dox regression tests in CI (open source)
- Ship every feature in self-host as well as paid-host
- Respond to security issues within 48 hours
- Best-effort support for general questions

---

## 6. When accounts get suspended

```
Scope Suspension:
  context violation: Violation
  context account: Account

  ## Triggers for suspension
  If violation is "harassment-of-attendees":
    Then action is immediate_suspension
    And appeal is permitted

  If violation is "bulk-export-attempt":
    Then action is immediate_suspension
    And appeal is permitted

  If violation is "spam-via-our-infrastructure":
    Then action is immediate_suspension
    And appeal is permitted

  If violation is "impersonation":
    Then action is immediate_suspension
    And appeal is permitted

  ## Due process
  If account is suspended:
    Then notice is given within 24 hours
    And reason is specific (not "terms violation" alone)
    And data export is provided before deletion
    And appeal process exists: email scott@scoble.me
    And appeal response is within 7 days
    And if appeal succeeds, account is restored
```

**In plain English:**

We will suspend your account if you:

- Harass attendees
- Attempt to bulk-export member data
- Send spam through our infrastructure
- Impersonate another organizer

When we suspend:

- We tell you within 24 hours
- We tell you the specific reason
- We let you export your data before deletion
- You can appeal to scott@scoble.me
- We respond to appeals within 7 days
- If your appeal succeeds, we restore your account

---

## 7. Liability (and its limits)

```
Scope Liability:
  context harm: Harm
  context responsible: Party

  ## What we are liable for
  If harm is "data-breach-caused-by-our-code":
    Then responsible is "us"
    And remedy is "notification-within-72-hours-and-reasonable-mitigation"

  If harm is "anti-dox-violation-caused-by-our-code":
    Then responsible is "us"
    And remedy is "fix-within-30-days-and-notify-affected-users"

  ## What we are NOT liable for
  If harm is "organizer-misconduct":
    Then responsible is "organizer"
    (We provide tools. We are not the organizer of your events.)

  If harm is "third-party-content":
    Then responsible is "third-party"
    (Other federated instances, imported content, AI suggestions — not our content.)

  If harm is "downtime-beyond-best-effort-99-percent":
    Then responsible is "no-one"
    And remedy is "self-host-if-you-need-SLA"
    (We don't promise five-nines. Self-host if you need it.)

  If harm is "data-loss-due-to-force-majeure":
    Then responsible is "no-one"
    And remedy is "backup-restore-from-your-own-backups"

  ## Cap on liability
  If liability applies to us:
    Then maximum is "amount-paid-by-user-in-prior-12-months"
    And minimum is "zero-for-free-tier-users"
    (If you didn't pay us, you can't sue us for damages. You can still get your data out and leave.)
```

**In plain English:**

- We are liable for data breaches caused by our code. We will notify you and mitigate.
- We are NOT liable for organizer misconduct (we provide tools, not policing of every group).
- We are NOT liable for content from other federated instances, imported content, or AI suggestions.
- We do NOT guarantee five-nines uptime. If you need SLA, self-host or use paid-host.
- Our maximum liability is what you paid us in the prior 12 months. If you're on the free tier, that's zero — but you can always export your data and leave.

---

## 8. Self-hosting terms

This is the section that matters for the v0.4 self-host parity policy:

```
Scope SelfHost:
  context operator: Person
  context instance: Instance

  ## License
  If operator runs instance:
    Then license is "AGPL-3.0"
    And operator can: modify-source, redistribute, run-commercially
    And operator must: disclose-source-modifications, preserve-copyright

  ## Operator-user relationship
  If operator has users:
    Then operator is data_controller for those users
    And operator writes their own terms and privacy policy (we provide a template)
    And we have no authority over operator's users
    And we have no liability for operator's actions

  ## What we provide
  If operator needs help:
    Then community support is available
    And consulting is available (paid) for federation / AI / self-host-at-scale patterns
    And operator is not obligated to use our support

  ## What we do NOT do
  If operator runs instance:
    Then we do not: access-operator-data, contact-operator-users, control-operator-instance
    And we do not require: telemetry, license-fee, attribution
    (Attribution is appreciated but not required.)
```

**In plain English — if you self-host:**

- AGPL-3.0 license. You can modify, redistribute, run commercially. You must disclose source modifications.
- **You are the data controller for your users.** You write your own terms and privacy policy (we provide a template).
- We have no authority over your users and no liability for your actions.
- We provide community support. Consulting is available for a fee.
- We do not access your data, contact your users, or control your instance.
- We do not require telemetry, license fees, or attribution (attribution is appreciated).

---

## 9. Termination

```
Scope Termination:
  context who: Party
  context reason: Reason

  If who is "user" and reason is "any":
    Then user can: export-data, delete-account
    And we honor the request within 30 days

  If who is "us" and reason is "terms-violation":
    Then we follow §6 suspension process

  If who is "us" and reason is "service-shutdown":
    Then we give 90 days notice
    And we provide data export tools
    And we publish the source code (already AGPL-3.0)
```

**In plain English:**

- You can leave anytime. Export your data, delete your account. 30 days.
- We can suspend you for terms violations, per §6.
- If we shut down the service, we give 90 days notice, provide data export, and the source is already open.

---

## 10. Disputes

```
Scope Disputes:
  context dispute: Dispute

  If dispute arises:
    Then first step is "direct-resolution-by-email"
    And email is scott@scoble.me
    And response is within 7 days
    And if not resolved, then mediation is the next step
    And mediation is in [jurisdiction]
    And if mediation fails, then arbitration or small-claims-court is the next step

  ## User-favoring defaults
  If dispute is "ambiguous-in-terms":
    Then resolution is interpret-in-favor-of-user (per §0)

  If dispute is "user-vs-organizer":
    Then we are not the arbitrator (we are not the organizer of every event)
    And we provide dispute-resolution-tooling (e.g., report abuse, request data export)

  If dispute is "user-vs-greybeard":
    Then we follow §0 user-favoring defaults
    And we provide transparent documentation of any past disputes and resolutions
```

**In plain English:**

- First step: email scott@scoble.me. We respond within 7 days.
- If we can't resolve it directly, mediation is next.
- We're not the arbitrator of user-vs-organizer disputes (we can't be). We provide reporting tools.
- For user-vs-Greybeard disputes, the user-wins principle from §0 applies.
- We publish resolutions to past disputes (transparency).

---

## 11. Changes to these terms

```
Scope TermsChanges:
  context change: TermsChange

  If change is "substantive":
    Then notice is given 30 days in advance
    And notice is to active users
    And if user does not consent, user can export-and-leave
    And we do not unilaterally impose changes that reduce user rights

  If change is "editorial-typo" or "clarification":
    Then notice is given at next login
```

**In plain English:** If we change these in a way that affects your rights, we give you 30 days notice. If you don't agree to the new terms, you can export your data and leave. We will not reduce your rights in a substantive change without notice.

---

## 12. Contact

- **Email:** scott@scoble.me
- **Response time:** 48 hours for security, 7 days for general
- **Escalation:** Scott Scoble, Greybeard Holdings LLC

---

## Appendix A: Test mapping

| Terms section | Test location |
|---|---|
| §2 authentication | `internal/web/authz_hardening_test.go` |
| §3 acceptable use | `internal/web/middleware.go` + acceptance tests |
| §4 organizer duties | `internal/web/anti_dox_test.go` |
| §5 our commitments | `internal/web/metrics.go` + status page |
| §6 suspension | `internal/admin/suspension_test.go` (TBD) |
| §7 liability | `docs/legal/liability-review.md` (TBD, requires lawyer) |
| §8 self-host | `LICENSE` (AGPL-3.0) + `docker-compose.yml` |

---

## Appendix B: For self-hosters

If you operate a self-hosted instance, **these terms do not bind your users to us.** Your users have a relationship with you, under your terms. We provide:

- This document as a **template** you can adopt
- The PRIVACY.md document as a **template** for your privacy policy
- A recommendation to **name your own legal entity and contact** in your terms
- A recommendation to **publish your own data residency and breach notification** commitments

We do not require attribution, telemetry, or license fees. The AGPL-3.0 license applies to source code modifications only, not to your operation of the unmodified software.

---

*This document is written in the Catala idiom — conditions before conclusions, deterministic outcomes, every commitment maps to a test. Real Catala compilation requires legal expertise we don't claim to have. A lawyer should review this before relying on it in a dispute. The Catala-style structure is here so that, when we do have legal review, the translation to enforceable terms is straightforward.*
