I could fetch the web, and verified the recent/uncertain citations. Evidence tags below mean: strong empirical = replicated or industrial/controlled evidence; mixed = evidence exists but context-dependent; practitioner-consensus = respected design guidance with limited direct validation; contested = early, vendor, or conflicting evidence.

**Top 8**
1. **Optimize for small change surface, not more code.** Strong empirical. Size and change history dominate many maintainability/defect models; Graves et al. found change history more useful than product metrics, Nagappan/Ball found relative churn predictive, and Sjøberg/Anda/Mockus found only size and low cohesion strongly associated with maintenance effort. AI teams should prefer modifying/reusing existing code over adding parallel implementations.

2. **Control coupling and dependency cycles.** Strong empirical. Dependencies propagate change, and cyclic components are more defect-prone; Geipel/Schweitzer show dependency/co-change links, Oyetoyan et al. show defects concentrate in cycle-related components. Your observation that fine-grained decomposition creates cycles is a real maintainability risk.

3. **Use deep, cohesive modules with stable interfaces.** Practitioner-consensus backed by coupling evidence. Parnas’s information hiding remains the best human-ground-truth framing: hide volatile decisions, not just split code. Ousterhout’s “deep modules” is a useful corrective to Clean Code’s small-function dogma: small units help only when they reduce reasoning, not when they create pass-through layers, fan-out, or shotgun navigation.

4. **Make names carry the domain model.** Strong empirical for comprehension, mixed for downstream outcomes. Feitelson et al. show naming is hard, non-convergent, and improved by explicitly choosing concepts, words, and construction; descriptive compound identifiers improve comprehension in ICPC work. Names are semantic beacons: they reduce working-memory reconstruction.

5. **Keep local code chunkable and idiomatic.** Mixed-to-strong comprehension evidence. Soloway/Ehrlich’s “plans” and “rules of programming discourse,” beacon studies, and fMRI work all point the same way: humans recognize familiar structures. Avoid cleverness, inconsistent idioms, deep nesting, and bumpy control flow. Cognitive complexity is closer to this than raw cyclomatic complexity.

6. **Treat comments as rationale infrastructure, not density.** Mixed empirical; strong practitioner consensus. Comment volume is a bad target. Experiments show comments help in some contexts and not others, especially for small tasks; newer eye-tracking work finds comments redirect attention and help when they summarize, clarify intent, or explain non-obvious context. “Self-documenting code” is valid for what, not for why, constraints, tradeoffs, or external invariants. Zero comments in all AI artifacts is not automatically a fail, but it is suspicious for nontrivial apps.

7. **Evaluate tests by fault-detection and diagnosability, not volume.** Strong empirical. Inozemtseva/Holmes found coverage only low-to-moderately correlated with effectiveness once suite size is controlled; mutation score has better evidence as a proxy for real-fault detection, though imperfect. Flaky tests undermine regression value. Your finding that test volume is uncorrelated or inverted with correctness fits the literature.

8. **Reduce surprise through consistency.** Mixed empirical; strong practitioner consensus. Formatting findings are old/mixed, but conventions aid comprehension through predictability. Naturalness-of-code work shows code is repetitive and predictable; naming/style inconsistency forces readers out of recognition mode into inference mode.

**Metrics Caveat**
LOC is not “the” construct, but it is the confounder everyone must respect. El Emam et al. showed class size can erase apparent OO metric effects; Sjøberg et al. found many maintainability metrics inconsistent. Cyclomatic complexity is still useful for local triage, but much of its signal overlaps with size, especially when aggregated; Landman et al. nuance this by showing method-level CC/SLOC correlation is only moderate, so do not discard CC, just do not overweight it.

SIG/Sigrid-style models are useful because they are repeatable and benchmarked against large corpora, but they are still surrogate models. Ghost Echoes 2024 is especially relevant: it benchmarks maintainability predictors against human assessments and warns that SonarQube maintainability ratings can produce many false positives. Use Sonar as a screen, not as ground truth.

AI-era evidence is early and mixed. GitClear’s 2024/2025 vendor reports show more churn, clones, and less moved/reused code, but attribution/confounding is contested. Borg et al.’s Echoes of AI controlled study found no systematic downstream maintainability penalty, while warning about code bloat. The 2026 CodeThread preprint reports agents perform worse when building on agent code, with drops up to 13.1%, but this is still preliminary.

**Mapping To Your Instruments**
Captured well: file/function size, cyclomatic/cognitive complexity, duplication, import cycles, fan-in/out, Semgrep/security smells, sampled mutation testing, and LLM-judge naming/abstraction/error-handling.

Captured with caution: SonarQube smell density, because smell density is size-sensitive and Ghost Echoes questions Sonar as maintainability ground truth. Cyclomatic complexity should be interpreted with LOC. Test volume should be de-emphasized; mutation and oracle quality matter more.

Not captured well, top 3 human needs:

1. **Change locality for real tasks.** Measure with blinded maintenance scenarios: give a maintainer a change request, record time-to-locate, files touched, edit distance, number of concepts/modules crossed, and correctness.

2. **Rationale and invariant recoverability.** Measure with a “why audit”: ask judges to identify non-obvious design decisions, invariants, error semantics, and rejected alternatives from code/comments/tests/docs; count missing or misleading rationale at module boundaries.

3. **Test failure locality and determinism.** Measure by running tests repeatedly with randomized order/time seeds, injecting small faults, and scoring flaky rate, assertion specificity, and how directly the failing test points to the faulty behavior.

**Rubric Lines**
1. **Naming and beacons:** “Identifiers, files, public APIs, and tests use the spec’s domain vocabulary consistently; a maintainer can infer each unit’s role without tracing implementation details or decoding generic names.”

2. **Abstraction and locality:** “Modules hide coherent design decisions behind stable interfaces; decomposition reduces the number of files/concepts needed for a likely change and does not introduce pass-through layers, circular dependencies, or shotgun edits.”

3. **Rationale and verification:** “Non-obvious behavior, invariants, error-handling choices, and external constraints are documented or asserted; tests are deterministic executable examples with meaningful oracles and localized failures.”

**Sources**
Feitelson et al., “How Developers Choose Names,” IEEE TSE 2022, https://doi.org/10.1109/TSE.2020.2976920  
Soloway & Ehrlich, “Empirical Studies of Programming Knowledge,” IEEE TSE 1984, https://doi.org/10.1109/TSE.1984.5010283  
Siegmund et al., “Measuring Neural Efficiency of Program Comprehension,” ESEC/FSE 2017, https://brains-on-code.github.io/  
Sjøberg, Anda & Mockus, “Questioning Software Maintenance Metrics,” ESEM 2012, https://doi.org/10.1145/2372251.2372269  
El Emam et al., “The Confounding Effect of Class Size…,” IEEE TSE 2001, https://doi.org/10.1109/32.935855  
Landman, Vinju & Serebrenik, “Relationship Between CC and SLOC,” ICSME 2014, https://ir.cwi.nl/pub/22662  
Graves et al., “Predicting Fault Incidence Using Software Change History,” IEEE TSE 2000, https://doi.org/10.1109/32.859533  
Nagappan & Ball, “Relative Code Churn…,” ICSE 2005, https://doi.org/10.1145/1062455.1062514  
Bird et al., “Don’t Touch My Code!,” FSE 2011, https://doi.org/10.1145/2025113.2025119  
Parnas, “On the Criteria…,” CACM 1972, https://doi.org/10.1145/361598.361623  
Oyetoyan et al., “Cyclic Dependencies…,” JSS 2013, https://doi.org/10.1016/j.jss.2013.07.039  
Inozemtseva & Holmes, “Coverage Is Not Strongly Correlated…,” ICSE 2014, https://doi.org/10.1145/2568225.2568271  
Just et al., “Are Mutants a Valid Substitute…,” FSE 2014, https://homes.cs.washington.edu/~mernst/pubs/mutation-effectiveness-fse2014-abstract.html  
Borg et al., “Ghost Echoes Revealed,” ICSME 2024, https://arxiv.org/abs/2408.10754  
Borg et al., “Echoes of AI,” EMSE 2026, https://doi.org/10.1007/s10664-026-10889-1  
GitClear AI Code Quality Reports 2024/2025, https://www.gitclear.com/ai_assistant_code_quality_2025_research