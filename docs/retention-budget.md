# Retention and compaction budget contract

This is a clear break: `Summarizer.Summarize` receives `SummaryRequest`, and
`BudgetPipeline.Apply` / `ApplyWithLimit` return `BudgetResult`. Role retention
moves from `DropHeadConfig.ProtectedRoles` to `BudgetConfig.Retention.Roles`.
There is no legacy callback or strategy-specific protection mode.

## Required content

`RetentionPolicy` selects mandatory messages by ID, exact `ContentRef`, or role.
Selectors are unioned; an explicit ID/ref absent from the budget input is an
error. Every mandatory message, including expanded round participants and rolling
or pending tails, needs a stable, unique ID. Anonymous optional messages are allowed.
References include the entire
message content and metadata, using the trace codec, reporter codec, or default
semantic codec (in that order). Missing host codecs fail explicitly.

Any selected participant protects its complete tool round. Pending tool rounds
and the chronological rolling tail remain protected independently of selectors.
Retention means byte/semantic preservation, not trust or authorization.

Mandatory messages are excluded from summarization and eviction callback inputs.
They remain in original order. A summary of noncontiguous evictable messages is
placed at the first evictable position; retained messages keep their relative
chronology. Eviction results are merged at their original positions. A callback
cannot reuse or replace a retained ID. When merging interleaved required content,
eviction must return an unchanged input subsequence; only summarization may
introduce a replacement message. The final output
must preserve all mandatory messages exactly. An impossible mandatory remainder
returns `ErrRetentionExceedsBudget`; a violated selection or preservation rule
returns `ErrInvalidRetention`. `MinMessages` cannot delete mandatory content.

## Hard capacity and soft compaction

The hard limit is the effective capacity for this pipeline input, after the
compile reserves other segments and the current turn. `CompactionPolicy` has a
pinned descriptor and integer `TriggerPercent` / `TargetPercent` in 1..100, with
target <= trigger. Both are percentages of this hard effective capacity, rounded
down without multiplication overflow. Nil policy triggers only on hard overflow
and targets the hard limit. A configured policy requires a summarizer.

Compaction starts when the initial input exceeds the trigger, or the hard limit.
An input at or below the trigger is returned without a summary call. Protected
content is never deleted to reach the soft target. Its cost is subtracted from
the hard and target capacities before constructing `SummaryRequest`.

`SummaryRequest.Messages` is owned input; `MaxTokens` is the hard remaining
capacity and `TargetTokens` is the nonnegative desired remaining capacity.
`Purpose` is the compaction/rolling/capture policy descriptor, or the intrinsic
whole-block summary descriptor. The host owns the summarization algorithm and
semantic requirements. At most one summary call occurs. A result exceeding its
hard maximum fails instead of silently deleting that summary. A result above
the soft target but within the hard maximum succeeds with `TargetReached=false`.
When required content alone exceeds the target, the summary target is zero;
the same hard-capacity rule still applies. Nonadditive estimates are checked
again on the complete merged output.

`BudgetResult` returns messages and `BudgetDecision`: actual hard/trigger/target
capacities, initial/final counts, protected content refs, summary request limits,
whether compaction ran and whether the soft target was reached. Decisions are
also returned by compilation per main/target channel, including without record
capture. They describe the budget stage; the independently final-counted output
may change after later patches/formatters.

## Identity and replay

Retention selectors, compaction policy, actual required refs and budget decision
participate in compile manifest identity. Captured compaction records include
the summary request and retention/compaction execution configuration. Exact
replay validates the current expectation and restores accepted saved outputs;
it does not repeat summarization, estimation or eviction. Callback mutations
cannot change source snapshots or configuration. Invalid policy is rejected
before callbacks and state loading.

No model prompts, relevance scoring, retrieval, automatic blob writes, provider
tokenizers or repeated LLM retries are implemented by this contract. Counts keep
the estimator's declared quality; successful budgeting is not a guarantee of
provider-exact tokenization.
