# What Actually Makes Code Maintainable for a Human: An Evidence-Ranked Synthesis

(Research agent: Opus, 2026-08-07, web-verified. Verbatim as returned.)

**On the evidence base.** This field is weaker and more vendor-entangled than its confidence suggests. Hall et al. found **83% of 208 fault-prediction studies too poorly reported to include in a review of their own field**. Shepperd et al. found **research group explains 31% of result variance; choice of classifier 1.3%**. And in the AI-era literature specifically, Agarwal et al. (CMU) report that their repo-mining trends on agent-authored PRs **"flip direction under different but equally defensible analysis choices."** Treat effect signs, not magnitudes, as the durable content.

---

## 1. Program comprehension

**Naming is the best-supported lever in the field — two independent professional-subject experiments.**

- **[Strong]** Hofmeister et al. (SANER 2017/EMSE 2018), **72 professional C# developers**: full-word identifiers gave **19% faster** defect finding than letters/abbreviations. Schankin et al. (ICPC 2018), **88 developers**: **14% faster** semantic-defect finding, effect present for experienced developers only.
- **[Strong]** Feitelson et al. (TSE 2020, N=334): two developers pick the same name only **6.9% of the time**, yet a chosen name is usually understood. **Low-consensus, high-comprehensibility.** Their 3-step model produced names judged superior **2:1** blind, by pushing toward *more concepts and longer names*.
- **[Strong, causal]** Gopstein's **atoms of confusion**: 19 patterns reliably causing misreading (N=73; Java replication N=132), occurring **~once per 23 lines** in 14 major C/C++ projects.
- **[Mixed→strong]** Peitek et al. (ICSE 2021 fMRI, N=19, 41+ metrics): what loaded on cognitive effort was **textual size and vocabulary size**, not control flow — "neuro-scientific evidence supporting warnings... questioning the validity of code complexity metrics."
- **[Classic, never overturned]** Letovsky & Soloway's **delocalized plans** (1986): scattering the lines that implement one coherent idea degrades comprehension.
- **[Practitioner-consensus only]** "4±1 chunks" applied to code is analogy. The *Cognitive Load is what matters* essay explicitly disclaims empirical grounding.

---

## 2. What predicts real maintenance outcomes

**The most relevant study to your setup is a null result.** Sjøberg et al. (TSE 2013): six *hired professionals*, 3 tasks each over 3–4 weeks, on **four functionally equivalent Java systems built independently from the same spec** — structurally your experiment. Per-file effort measured automatically: 298 files, 189.4 hours. Adjusted R²: controls 0.15 → +smells 0.36 → +file size 0.42 → **+change count 0.58**. Verbatim: "**code smells are not needed to explain the maintenance effort if we adjust for file size and the number of changes**"; file size alone beats all 12 smell predictors. *N=6, unreplicated.*

**Size dominates — five converging lines.** El Emam et al. (TSE 2001): OO metrics correlate with LOC at 0.86–0.88; significance vanishes or flips once LOC enters. Two corrections: the circulated "only 4 of 24 survive" line **is not in the paper** (it says *none*), and univariate R² was **0.014–0.055** — 1–6% of variance explained before controlling for anything. *[Contested on mechanism: Evanco argues size is a mediator, not confounder; Tahir et al. find effects metric-specific.]*

**The metrics-vs-human gap — my initial framing was wrong.** Borg, Ezzouhri & Tornhill, **"Ghost Echoes Revealed" (ICSME 2024, arXiv 2408.10754)** exists and does *not* show weak metric-human correlation. Against 304 Java files labeled by ≥3 of 70 professionals:

| Approach | F1 | AUC |
|---|---|---|
| CodeScene Code Health | 0.96 | 0.95 |
| SotA ML (AdaBoost) | 0.95 | 0.97 |
| **Plain LOC threshold (≤275 lines)** | **0.95** | **0.95** |
| Maintainability Index | 0.90 | 0.89 |
| **SonarQube Maintainability Rating** | **0.75** | **0.60** |

A 275-LOC threshold ties state-of-the-art ML (the paper concedes it). SonarQube's shipped rating is near-chance. The "beats the average human expert" claim is structurally rigged — ground truth *is* the experts' majority vote. **Vendor-authored** (Tornhill founded CodeScene).

Three further demonstrations: **Scalabrino et al.** (TSE 2019) — 121 metrics, 57 participants, **none** captured understandability. **Readability PRs** — SonarQube detected **26 of 370 (7%)** developer-made readability improvements. **Code review** (arXiv 2410.21990) — **>42%** of 2,401 comments concern understandability; four linters cover **under 30%**.

**SonarQube is your weakest instrument.** Lenarduzzi et al. (SANER 2020, 39,518 commits): SonarQube's own "bug"-typed rules score **precision 0.086, recall 0.028, AUC 0.509 — chance.** Lenarduzzi et al. (JSS 2020, 33 projects): "clean classes are **slightly more change-prone**... for fault-proneness, **there is no difference**." Even Palomba et al.'s pro-smell study finds **smell presence alone is not significant for defects** after size control. Santos et al. (JSS 2018): "**human agreement on smell detection is low**."

**Your duplication instrument measures the wrong thing.** Rahman, Bird & Devanbu (MSR 2010/EMSE 2012): "the great majority of bugs are not significantly associated with clones... clones may be **less** defect prone than non-cloned code." Juergens et al. (ICSE 2009) found only **inconsistent** clones are fault-prone. **Duplication harms when copies diverge — jscpd measures duplication, not divergence.**

**Where large effects do live:** architecture-level coupling (Sturtevant/MacCormack: **~50% productivity drop, ~3× defect density, order-of-magnitude turnover** core vs periphery). Ceiling check: in Bird et al.'s Windows data, **all classic code metrics together explain 18–29% of failure variance.**

**SIG / Better Code Hub: [practitioner-consensus, not validated].** The 2007 paper contains **no empirical validation** — mappings are expert judgment, self-described as "work in progress." Its validation (Bijlsma 2012) is **pseudo-replicated** (89 "independent" snapshots from 10 systems), vendor-authored on both sides.

---

## 3. Comments and documentation — the section I initially had backwards

- **[Contested, trending null]** The best-powered modern experiment — **Nielebock et al. (EMSE 2019), N=277, mainly professionals** — found **no significant effect of comment condition on correctness**. Börstler & Paech (N=104): no comprehension difference, but significant *perceived readability* difference. Abdelsalam et al. (EMSE 2025 eye-tracking): effect ranged **−30% to +34%** by snippet; correctness improved significantly in only 1 of 12.
- **[Strong — most replicated finding here] Subjective appreciation of comments consistently exceeds measured benefit.** Same dissociation in Nielebock, Börstler, and Abdelsalam independently. **This is a direct threat to your LLM judge**, which will reward what humans *say* helps.
- **[Strong] Comment density inverts.** Aman et al. (3 studies): well-commented modules **2–8× more likely faulty**; effect survives size adjustment (**1.6–2.8×**). Mechanism: **~59% of comment lines are license/metadata/TODO/commented-out code** (Pascarella & Bacchelli).
- **[Convergent observational; no causal test] "Why not what."** Supply: rationale is **~2% of comment blocks**. Demand: LaToza et al. (ICSE 2006, N=104) — rationale was the **#1 serious problem, 66% agreement**; LaToza & Myers — **Rationale (42) and Intent (32) top two of 21** hard-question categories. Maalej & Robillard: Purpose/Rationale in **~5% of member-level API docs**, while **43–51% of member docs are "non-information"** boilerplate restating the name.
- **[Strong] Place it at the declaration site.** Head et al. (ICSE 2018, Google, in-situ): of developers seeking usage info, **61.7% wanted the answer in the header**.
- **[Strong] Comment rot.** Wen et al. (ICPC 2019, 1.3B AST changes): **only 13–20% of code changes update related comments.**
- **[Directly relevant]** Gloaguen et al. (2026, 138 tasks, 4 agents): **LLM-generated AGENTS.md context files gave no success improvement and +20–23% cost**; auto-generated repo overviews were useless; **explicit imperative instructions were followed.**

**Revised guidance: do not instruct for comments or density. Instruct for rationale at decision points, at the declaration site** — the one information type measured as most-demanded and least-supplied.

---

## 4. Modularity and architecture

- **Cycles: avoid** *[mixed-strong]*. Finer decomposition co-occurring with cycles is your most actionable signal: agents split by *size heuristics*, not dependency direction.
- **Information hiding (Parnas): consensus, thin direct validation.** Sullivan et al. (FSE 2001) is real-options *analysis*, not an outcome study.
- **Deep vs. shallow modules: unresolved** *[contested]*. Ousterhout and Clean Code are both untested. **Do not let your rubric assume either side.** The cross-cutting finding is delocalized plans.

---

## 5. Tests — and a finding that undercuts your mutation instrument

- **[Strong] Coverage is a weak proxy.** Inozemtseva & Holmes (ICSE 2014). **Your finding that test volume didn't correlate with verified correctness is a replication, not an anomaly.**
- **[Strong for human suites] Mutation score is the better proxy** (Just 2014; Papadakis 2018).
- **⚠️ [Single-study, but directly on point] Mutation score is NOT a safe proxy for LLM-generated suites.** Abdullin et al., *"Test Wars"* (arXiv 2501.10200): LLM approaches **fall behind on coverage, significantly outperform on mutation score, and perform worse than SBST/symbolic execution on real fault detection.** Zhao, Zhou & Cohen (ISSTA 2026, 8,268 suites / 101,123 tests on Defects4J): normalized mutation correlation with bug detection **r=0.493, p=0.087 — not significant**; proxies become unreliable "where the code-under-test may already be buggy."
- **⚠️ [The single most important AI-era finding for your design] The "Self-Repair Trap."** Li, Yu & Yuan (ASE 2026, arXiv 2608.05917): LLM test generation is framed as *regression-oracle completion* — "the current program version is treated as expected behavior," so a buggy implementation gets its bug **specified into the suite**. And: "**iterative repair progressively drives models toward assertions that are easier to satisfy but less effective at detecting faults.**" Corroborated by Konstantinou et al.: oracle correctness drops **8.4–9.5 points on buggy code**; LLMs "generate oracles that capture the actual program behaviour rather than the expected one." **Your agent teams iterate until green. This is documented to degrade the fault-detection power of the suite they produce.**
- **[Practitioner-consensus only]** Tests-as-documentation; descriptive names; failure locality. Little controlled backing.

---

## 6. Consistency, idiomaticity, surprise

- **[Mixed-strong] "Naturalness."** Hindle et al. (2012); Ray et al. (2016): **buggy code is measurably less natural (higher entropy)**, competitive with static analysis for defect localization. Best grounding for "surprise costs you" — and *directly measurable*.
- **Atoms of confusion** are the local, causally-demonstrated version.
- **[Practitioner-consensus, anecdotal]** Google's readability process reports practices, not controlled comparisons.

---

## 7. AI-era specifics — with the corrections that matter

**GitClear: I over-credited it, and so does everyone.** The flagship reports **never observe AI authorship** — trends are inferred from calendar time correlating with adoption curves, so every trend is confounded with 2020–2026 industry shifts. Its inferential chain (duplication → defects) rests on clone literature that points the other way (§2). Headline multipliers are irreconcilable across their own pages ("4x", "eightfold", "10x", "+81%" on the same small subsample). **No independent replication exists.** Their *honest* result is the 2026 cohort analysis with real telemetry: heavy AI users show **5.2× commits, 4.2× Diff Delta — and 9.4× churn, 4.1× copy/paste**, with selection effects the authors concede ("AI might not be the cause of extra output—it might be the companion to it").

**⚠️ METR has effectively withdrawn the 19%.** The 2025 RCT (N=16, 246 tasks, mature repos) found AI *increased* completion time 19% against a forecast of −24%. But the **2026 update** reports severe selection effects (30–50% of developers declined to submit tasks they didn't want to do without AI); re-run cohorts gave **−18% (CI −38% to +9%)** and **−4% (CI −15% to +9%)** — both spanning zero, which METR calls "only very weak evidence." **Do not cite "-19%" as current.** The durable finding is the **~39-point perception/reality gap**, and the scope condition: subjects were experts in code they knew extremely well — the *opposite* of your handover scenario.

**[Strong, causal, and closest to your concern]** Shen & Tamkin (Anthropic, arXiv 2601.20245): randomized, N=52, learning an unfamiliar async library. **Skill formation significantly reduced, Cohen d=0.738, p=0.01 (~17% quiz-score difference)**, with **no significant efficiency gain**. An Anthropic-authored result unfavorable to naive AI use, which strengthens it.

**[Best matched-control study on AI code structure]** Zhu, Tsantalis & Rigby (arXiv 2605.02741) — the only study with a **human baseline**: Long Method counts Qwen-Coder-480b **11–13**, Gemini-2.5-Pro **5–8**, **human 1**. **And prompt specificity had no effect (p>0.8).** *That is direct evidence against the assumption behind your architect-agent experiment: telling a model to produce maintainable code, via prompt detail alone, did not change architectural smell counts.*

**[Largest real-codebase study]** Liu et al. (arXiv 2603.28592): **302,579 AI-authored commits, 6,299 repos**, AI identified from explicit git metadata. Of 484,366 issues: **89.3% code smells, 6.0% correctness, 4.7% security**; 22.7% persist at HEAD. Underreported nuance: **AI *fixed* more smells (439,817) than it introduced (432,748)** — net-positive on smells, net-negative on correctness and security. **This matches your data exactly: structural metrics look acceptable while externally-verified correctness does not follow.** Sonar's 4,442-task study independently finds **pass@1 does not predict quality**.

**Review behavior — one popular claim is unsupported.** Singh et al. (FSE 2026, **N=385**, 2×2 complexity × provenance): **complexity increased over-compliance; the AI-provenance label had no main effect.** Khojah et al. (ASE 2026, N=32 professionals, eye-tracking): the AI label increased *fixation duration* (CI [0.09, 0.56]) but **not saccade length — the thoroughness proxy (CI [−0.07, 0.10])**. An AI label buys time on task, not deeper scanning. **Anchoring is the real effect:** Tufano et al. (N=29, >50h recorded reviews) — "reviewers tend to focus on the code locations indicated by the LLM rather than searching for additional issues"; more low-severity issues found, **not more high-severity**, no time saved.

**Useful positive:** *Code for Machines, Not Just Humans* (FORGE 2026, 5,000 files) found human-calibrated code health associated with **semantic preservation after LLM refactoring** — your maintainability objective predicts agent-extensibility rather than trading against it.

**Denominators are not comparable.** Pearce's "40% vulnerable" = adversarial prompts (Siddiq found ~2% on HumanEval; a replication dropped 36.5%→27.3% with a newer model). Sonar's 90% = share of *findings*, not of code. GitClear's 12.3% = share of *changed lines*. Quoting these side by side is meaningless — and the Perry (CCS 2023) security result is **not significant after Benjamini–Hochberg correction (p=0.056)**, with Sandoval et al. finding the opposite on a different task class.

---

## 8. Synthesis for your experiments

### (a) Ranked top-8

1. **Name things with enough concepts; never abbreviate.** 19% (N=72) and 14% (N=88) faster defect finding, professional subjects; Feitelson's model yields 2:1 better names. *[Strong]*
2. **Keep files small.** A 275-LOC threshold ties SotA ML at predicting expert maintainability judgment; file size beats all 12 smell predictors at explaining measured effort. *[Strong, converging]*
3. **Eliminate import-graph cycles.** *[Mixed-strong; your observed failure mode]*
4. **Minimize vocabulary, not just branching.** *[Strong]*
5. **Keep one coherent idea co-located.** Split by dependency boundary, not line count. *[Classic-strong]*
6. **Rationale at decision points, at the declaration site.** ~2% of comments but the #1 measured unmet need; 61.7% want it in the header. *[Convergent observational]*
7. **Ban known confusion constructs.** *[Strong, causal]*
8. **Be boring and internally consistent.** *[Mixed-strong]*

**Do NOT instruct for:** comment density (inverts, 2–8× fault association), smaller functions per se (untested), coverage percentage (weak proxy), or raw duplication count (only *divergent* clones are fault-prone).

### (b) Instrument coverage and gaps

Covered: file size ✅ (strongest validated), cycles ✅. Weakly: naming (judge only). **Not covered: vocabulary size, co-location, rationale, consistency.**

**Three instrument corrections, in priority order:**

1. **Heavily downweight SonarQube smell/debt density.** Shipped bug rules AUC 0.509 (chance); maintainability rating 0.60; no clean-vs-dirty fault difference; 7% detection of real readability improvements; and smells add *nothing* over file size + change count in the best-instrumented effort study.
2. **Change what jscpd feeds.** Duplication per se is not established as harmful (Rahman/Bird/Devanbu). Measure **divergent duplication**: clone groups whose members are *near*-identical but not identical, and clone groups that drift apart across commits. That is the construct Juergens validated.
3. **Do not read mutation score as suite quality for LLM-written suites.** LLM suites score *higher* on mutation while detecting *fewer* real faults, and iterative green-loop repair actively selects for easy-to-satisfy assertions ("Self-Repair Trap"). **Fix:** score the team's suite against **externally-authored seeded faults** it never saw, not against mutants of its own code.

**The three things humans need that nothing you have captures:**

1. **Rationale recoverability (not comment density).** Operationalize as an **extraction task, not a judge score**: give a fresh model a module, ask it to enumerate design decisions and justifications; score = fraction with a recoverable *why*. Verifiable, unlike "is the naming good?"
2. **Internal consistency / surprise.** Compute **cross-file perplexity** — score each file with a code LM *conditioned on the rest of the repo*. High-perplexity files are where the codebase contradicts itself. Deterministic, grounded in the naturalness result, and it directly catches multi-agent idiom drift.
3. **Comprehension-under-modification — the actual ground truth.** Hold out N spec-derived change requests; give a *fresh* agent (or human) the codebase cold; record **time-to-correct-change and first-attempt correctness**. This is Sjøberg's design, the strongest in the field precisely because it did this. **If you add one instrument, add this one.**

**A design warning for the architect-agent arm:** prompt specificity alone showed **no effect on architectural smells (p>0.8)** in the only matched-baseline study. Build the arm so it can distinguish "the architect agent had authority to reject and rework" from "the architect agent was told to care about maintainability" — the latter has direct evidence against it.

### (c) Three anchored rubric lines

Anchor to recoverable facts. The perception/performance dissociation means an unanchored judge will reward comment presence, tidiness, and small functions — none of which have measured human benefit.

> **R1 — Rationale recoverability (add; weight high).** "For each of the 5 most complex modules, list every non-obvious design decision (data structure, concurrency approach, error policy, external-contract assumption). Score 0–4 by the fraction whose *justification* is recoverable from code, comments, or tests: 4 = ≥80%; 2 = ~50%; 0 = decisions present, no rationale anywhere. **Do not credit comments that restate what the code does, license headers, or TODOs.** Credit rationale at the declaration site over rationale buried in the body."

> **R2 — Naming information content (reweight; replaces 'good naming').** "Sample 20 identifiers (10 functions, 10 non-trivial locals). Judge whether each name encodes *all* concepts needed to predict its role without reading the body. Flag single letters, unexplained abbreviations, and type-only names (`data`, `result`, `handler`, `manager`). Score = fraction passing. Longer multi-concept names are correct answers, not verbosity."

> **R3 — Idiom consistency / least astonishment (add).** "Identify the codebase's dominant convention for each of: error propagation, async style, module export shape, validation placement, naming case. Report the fraction of sites following each. Score 0–4 on the **minimum** of the five fractions. A codebase where each subsystem invented its own approach scores 0 even if every subsystem is individually clean."

---

## Sources

**Metrics/outcomes:** Ghost Echoes https://arxiv.org/abs/2408.10754 · Sjøberg et al. https://www.mn.uio.no/ifi/personer/vit/dagsj/sjoberg_etal_code-smells.pdf · Heitlager et al. https://webarchive.di.uminho.pt/wiki.di.uminho.pt/twiki/pub/Personal/Joost/PublicationList/HeitlagerKuipersVisser-Quatic2007.pdf · Bijlsma et al. https://doi.org/10.1007/s11219-011-9140-0 · van Deursen https://avandeursen.com/2014/08/29/think-twice-before-using-the-maintainability-index/ · El Emam et al. https://www.ehealthinformation.ca/web/default/files/wp-files/1062.pdf · Tahir et al. https://doi.org/10.1007/s10664-021-09991-3 · Hall et al. https://doi.org/10.1109/TSE.2011.103 · Shepperd et al. https://eprints.lancs.ac.uk/id/eprint/127414/1/Bias.pdf · Lenarduzzi SANER'20 https://arxiv.org/abs/1907.00376 · JSS'20 https://arxiv.org/abs/1908.11590 · Palomba et al. https://fpalomba.github.io/pdf/Journals/J9.pdf · Santos et al. https://doi.org/10.1016/j.jss.2018.07.035 · Rahman/Bird/Devanbu https://doi.org/10.1007/s10664-011-9195-3 · Juergens et al. https://teamscale.com/publications/2009-do-code-clones-matter.pdf · Sturtevant https://dsmsuite.github.io/external/CostOfComplexityReport.pdf

**Comprehension/naming:** Feitelson https://arxiv.org/abs/2103.07487 · Hofmeister https://doi.org/10.1109/saner.2017.7884623 · Schankin https://dl.acm.org/doi/10.1145/3196321.3196332 · Peitek ICSE'21 https://conf.researchr.org/details/icse-2021/icse-2021-papers/10/Program-Comprehension-and-Code-Complexity-Metrics-An-fMRI-Study · Scalabrino https://www.cs.wm.edu/~denys/pubs/TSE'19-Understandability.pdf · atoms of confusion https://ssl.engineering.nyu.edu/papers/gopstein_atoms_fse_2017.pdf · Letovsky & Soloway https://www.cs.kent.edu/~jmaletic/cs69995-PC/papers/letovsky-1986-software.pdf

**Comments/docs:** Nielebock https://link.springer.com/article/10.1007/s10664-018-9664-z · Abdelsalam https://link.springer.com/article/10.1007/s10664-025-10721-2 · Pascarella & Bacchelli https://sback.it/publications/msr2017a.pdf · LaToza et al. https://www.microsoft.com/en-us/research/wp-content/uploads/2016/02/p492-latoza.pdf · LaToza & Myers https://dl.acm.org/doi/10.1145/1937117.1937125 · Ko et al. https://www.microsoft.com/en-us/research/wp-content/uploads/2016/02/icse07_ko.pdf · Head et al. https://andrewhead.info/assets/pdf/when-not-to-comment.pdf · Wen et al. https://csnagy.github.io/research/pdfs/2019/Wen2019-preprint.pdf · Aman et al. https://ieeexplore.ieee.org/document/6363289/ · Maalej & Robillard https://www.cs.mcgill.ca/~martin/papers/ese2011.pdf · AGENTS.md eval https://arxiv.org/abs/2602.11988

**AI era:** METR 2025 https://arxiv.org/abs/2507.09089 · METR 2026 update https://metr.org/blog/2026-02-24-uplift-update/ · Shen & Tamkin https://arxiv.org/abs/2601.20245 · Zhu/Tsantalis/Rigby https://arxiv.org/abs/2605.02741 · Liu et al. https://arxiv.org/abs/2603.28592 · Sonar https://arxiv.org/abs/2508.14727 · Singh et al. https://dl.acm.org/doi/10.1145/3808165 · Khojah et al. https://arxiv.org/abs/2606.26505 · Tufano et al. https://arxiv.org/abs/2411.11401 · Agarwal et al. https://arxiv.org/abs/2607.07980 · Perry et al. https://arxiv.org/abs/2211.03622 · Sandoval et al. https://arxiv.org/abs/2208.09727 · Pearce et al. https://arxiv.org/abs/2108.09293 · GitClear 2025 https://www.gitclear.com/ai_assistant_code_quality_2025_research · 2026 https://www.gitclear.com/the_ai_code_quality_maintainability_gap · DORA 2024 https://dora.dev/research/2024/dora-report/ · 2025 https://cloud.google.com/blog/products/ai-machine-learning/announcing-the-2025-dora-report · Code for Machines https://arxiv.org/abs/2601.02200

**LLM tests:** Test Wars https://arxiv.org/abs/2501.10200 · Zhao/Zhou/Cohen https://arxiv.org/abs/2607.22880 · Self-Repair Trap https://arxiv.org/abs/2608.05917 · Konstantinou et al. https://arxiv.org/abs/2410.21136 · Ouédraogo et al. https://arxiv.org/abs/2407.00225 · Schäfer et al. https://arxiv.org/abs/2302.06527

**Cited from established literature, not re-fetched:** Inozemtseva & Holmes ICSE 2014; Just et al. FSE 2014; Papadakis et al. ICSE 2018; Hindle et al. ICSE 2012; Ray et al. ICSE 2016.

**Caveats on completeness:** the tests/consistency thread returned only partial results — the naturalness (Hindle/Ray) effect sizes, test-smell, and flaky-test numbers were not primary-verified this session. Values flagged unverifiable elsewhere: METR's exact 95% CI on the original 19%; Singh et al.'s effect magnitudes. **Highest-value unread item:** Chowdhury, Holmes, Zaidman & Kazman, *"Revisiting the debate: Are code metrics useful for measuring maintenance effort?"* EMSE 27 (2022), https://doi.org/10.1007/s10664-022-10193-8.
