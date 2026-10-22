# 39 — Execute: from a ready roadmap to a tested milestone

**Milestone focus:** 0.39. Statuses live in
[m_0.39_execute.md](../../roadmaps/v0.x/m_0.39_execute.md).

Chapter [33](33-delivery-ui.md) defines the Delivery surfaces and their schema. This chapter says
what happens when a person presses **Execute**: how a milestone moves through its lifecycle, how
phases become Jobs, how a test campaign runs and loops back into the roadmap, and when a milestone
may be accepted and merged. Vision §2, §4.3, §4.4, §6 and §7 in
[DELIVERY_PIPELINE_VISION.md](../../../DELIVERY_PIPELINE_VISION.md) state the same flow as a whole.
Every rule here is the one the store and the API enforce today; where the vision and the code
differ, this chapter follows the code and says so.

## 1. Two axes, not one

A milestone carries two separate states, and most of the rules below depend on which axis an action
touches.

| Axis | Values | Meaning |
|---|---|---|
| **Lifecycle** (`status`) | `awaiting_design`, `awaiting_roadmap`, `awaiting_development`, `in_development`, `testing`, `complete` | where the milestone is in its life |
| **Run** (`run_state`) | `idle`, `running`, `paused`, `stopped` | whether its phase Jobs are currently allowed to advance |

The lifecycle moves only on explicit actions. The run state moves when a Job finishes, is cancelled,
or fails, and when a person presses Start, Pause or Stop. Stopping a run therefore never changes the
lifecycle: it only changes `run_state`.

## 2. What 0.39 adds to 0.33

| Layer | 0.33 | 0.39 |
|---|---|---|
| Milestone | a lifecycle status, set by hand | the status advances by the execute and test actions below |
| Phase | a seeded row under a milestone | a Job: `job_id`, `job_status` and `job_last_event` on the phase row |
| Test | a queue of milestones in `testing` | a campaign: cases run, failures become a buglist, fixes re-run |
| Accept / merge | a checklist and a stub | an acceptance gate, then a merge of `milestone/{id}` into main |

The schema's shape does not change. The added columns are the `job_*` fields on phases, buglist rows
and polishes, and the `campaign_job_*` fields on the milestone. `delivery_merge_root` is the one new
setting, and it is read by the merge step only.

## 3. The lifecycle and its guards

Each action is a single `PATCH` on the milestone with one flag set. The store checks the guard for
that flag and refuses with a message that names the missing condition. An action that is not in the
table below is not available from the console.

| Action (flag) | From | To | Guard |
|---|---|---|---|
| Design approved | `awaiting_design` | `awaiting_roadmap` | set by the design step (0.33) |
| Ready (`ready_dev`) | `awaiting_roadmap` | `awaiting_development` | the milestone is bound, and at least one phase is unfinished (a bugfix or a planned phase) |
| **Execute** (`execute`) | `awaiting_development` or `in_development` | `in_development` | `execute_worker_id` is set, and the milestone has at least one phase |
| Submit test (`submit_test`) | `in_development` | `testing` | explicit; never implied by Stop |
| Pass (`pass_test`) | `testing` | `complete` | the test campaign is clean (§5) |
| Fail (`fail_test`) | `testing` | `awaiting_roadmap` | the milestone is bound and a title or body is given; a buglist item is added and bugfix phases follow |
| Accept (`accept_milestone`) | `complete` | `complete`, `accept_status = accepted` | every Acceptance item is checked (§7) |

Two details are easy to miss. Execute accepts both `awaiting_development` (the initial start) and
`in_development` (a restart), so it is not limited to one lifecycle edge. The phase list can be
bootstrapped from the Design Versions only before Execute; a milestone with no phases cannot be
executed.

## 4. Execute: bind, start, and roll back

Execute runs in three steps, and the last two are undone together if the first Job cannot start:

1. The Worker binding (`execute_worker_id`) is saved and the milestone is checked for phases.
2. The first phase Job is started. This step runs the Gate check (§5) before it enqueues anything.
3. Only if step 2 succeeds, the milestone is set to `in_development`. If step 2 fails, the previous
   status and run state are restored, so a failed Execute leaves the milestone exactly as it was.

Each phase Job is enqueued through the scheduler's job-assign API. The request carries
`milestone_id`, `phase_id`, `eng_req_id`, `major` and `minor` as pass-through fields
(`JobAssignBody`). The scheduler does not interpret them, so a Job that carries them runs the same way
as one that does not.

A phase row's `job_status` is one of `queued`, `running`, `finished`, `error` or `cancelled`.
`job_last_event` holds the most recent event text so the card can show it without opening the stream.

## 5. The run loop: auto-advance, pause, stop

When a phase Job finishes, the server decides what comes next from `run_state`:

| Job ends as | Phase row | `run_state` | Next phase |
|---|---|---|---|
| `finished`, and run is `running` | `finished` | stays `running` | the next phase starts automatically |
| `finished`, and no runnable phase remains | `finished` | stays `running` | none; the run waits for Stop or Submit test |
| `finished`, but the next phase is blocked by an unchecked Gate | `finished` | `paused` | none; the reason is recorded on the phase |
| `cancelled` (Stop) | `cancelled` | `stopped` | none |
| `error` (or any other status) | `error` | `paused` | none |

Pause and Stop ask the scheduler to cancel the running Job through
`POST /api/v1/jobs/{id}/cancel`. The anvil-web route reads the durable Job kind and dispatches to
the owning Workbench session or Delivery milestone. A cancel on a job that is no longer running
returns `409` (`job is not running`), so a stale click is reported rather than applied.

The Gate check is the one place a run can stop by itself. A Gate that is not satisfied does not fail
the phase; it pauses the run, and the person decides whether to fix the Gate and press Start again.

## 6. The test campaign loops back into the roadmap

The Test stage is a campaign, not a single run:

1. **Draft cases.** An AI assist drafts test cases from the milestone's acceptance text. A person
   accepts or edits them. Drafting writes nothing by itself.
2. **Run.** The campaign Job runs the cases on the milestone's Worker. Its result carries an
   `ANVIL_CAMPAIGN_RESULT:` line followed by a JSON object, which the server parses to record each
   case. A result without that line is not treated as a pass.
3. **Fail → buglist.** Each failed case becomes a buglist item bound to the requirement and to
   `major.minor`. A failed campaign moves the milestone to `awaiting_roadmap` through Fail, and the
   roadmap adds bugfix phases.
4. **Fix.** A Fix Job works one buglist item. The item moves through `open`, then `fixing`, then
   `verified`; its `job_*` fields record the Job.
5. **Regress.** Regress is refused unless at least one buglist item is `verified`
   (`no verified bugs to regress`). Otherwise it re-runs the cases against the fixes and stores its
   `campaign_job_*` fields on the milestone.
6. **Pass.** Pass is checked against the whole campaign, not only the last run. The check requires
   all of the following:
   - at least one test case exists;
   - every test case has status `passed`;
   - no buglist item is `open` or `fixing` (`verified` and `closed` are allowed).

A buglist item that is closed is never reopened by a pass; a failed case is never dropped to let a
campaign pass.

## 7. Accept and merge are separate gates

`complete` means the tests passed. It does not mean the milestone is accepted into main.

- **Accept** requires the milestone to be `complete`, and every item in its Acceptance checklist to be
  checked. The check is the same one the console shows as "acceptance checklist incomplete". On success
  `accept_status` becomes `accepted` and `accepted_at` is set. Accept does not merge anything.
- **Merge** requires `accept_status = accepted`. It runs once per milestone: a milestone whose merge
  already succeeded returns unchanged. The merge target is `milestone/{id}` into main.
  - If `delivery_merge_root` is set, the repository is that root joined with the project's path, and the
    merge runs real git (`LocalGitMerger`). The store does not check that the checkout exists; a missing
    or broken repository surfaces as a git failure, recorded as `merge_status = error` with the git
    message, and the request returns an error.
  - Otherwise, or when the request asks for `ForceStub`, the stub merger runs. It reports success and
    records `merge_status = merged` without touching any repository. A milestone merged this way has
    not changed the code.
- Merge is never triggered by Pass. Vision §6 keeps the policy: phases merge often into the milestone
  branch; the milestone branch merges into main once, after acceptance.

## 8. What a person sees

The roadmap and the test queue are views over the same rows; neither keeps its own state.

| Surface | Shows | Acts |
|---|---|---|
| Roadmap canvas | every milestone of the chosen major; its status and run state; the live phase Job | Execute, Start, Pause, Stop, Submit test |
| Roadmap inspector | the bound Worker, Gates, phases | bind the Worker, edit phases |
| Test queue | milestones in `testing` only | Run, Fix, Regress, Pass |
| Buglist | items and their status (`open`, `fixing`, `verified`, `closed`) | Fix |
| Accept panel | the acceptance checklist and `accept_status` | Accept, Merge |

A milestone that is not `testing` never appears in the test queue. Execute is shown only when the
milestone has a Worker and phases.

## 9. Non-goals

- Retrieval over the Delivery corpus. That is vision §8, not an execution concern.
- Auto-merge to main, and auto-submit to testing. Both need a person.
- A second execution engine. Phase and campaign Jobs run on the scheduler and the worker; this
  chapter adds the Delivery state around them.
- Branch policy beyond `milestone/{id}` → main. Stacked or parallel milestones are a later decision.
- Flutter or App consumption of execute state. The API is the contract; clients follow it later.

## 10. Acceptance

1. Execute without a Worker, or on a milestone with no phases, is refused with the missing condition
   named.
2. With an unchecked Gate item, a manual Start or Execute is refused with `400` and the message
   `gate checklist incomplete`, and nothing starts. During an automatic advance the same refusal
   instead pauses the run (`run_state = paused`).
3. A phase that finishes while the run is `running` starts the next phase; a cancelled Job sets
   `run_state` to `stopped`; any other end sets it to `paused`.
4. A failed campaign case creates a buglist item, and Fail moves the milestone to `awaiting_roadmap`.
5. Pass is refused while any test case is not `passed`, or any buglist item is `open` or `fixing`.
6. Accept is refused unless every Acceptance item is checked; Merge runs only after Accept, and a
   stubbed merge is recorded as `merged` without touching a repository.
7. Pausing or stopping a Job never changes the lifecycle status.
