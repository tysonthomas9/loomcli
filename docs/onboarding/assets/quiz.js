/* ---------------------------------------------------------------------------
   Loom Onboarding — retrieval practice component
   Link once per lesson:  <script src="../assets/quiz.js" defer></script>

   Three widgets, all authored declaratively in HTML. The script wires them and
   injects its own styles (it consumes the CSS custom properties from
   course.css, so it stays on-theme in light and dark).

   1. MULTIPLE CHOICE — immediate feedback, explanation on answer.

      <div class="quiz" data-quiz="0003">
        <div class="q" data-answer="2">
          <p class="stem">Which package spawns the embedded fleet-db?</p>
          <ul class="opts">
            <li>internal/store</li>
            <li>internal/backend</li>
            <li>internal/bootstrap</li>
          </ul>
          <div class="why">Because …</div>
        </div>
      </div>

      data-answer is the 0-based index of the correct option.
      Write options at equal word count — length is a tell, and a tell turns
      a retrieval exercise into a pattern-matching exercise.

   2. FREE RECALL — the highest-value widget. Recall from memory BEFORE seeing
      the answer, then self-grade. Effortful retrieval is what builds storage
      strength; recognising a right answer in a list does not.

      <div class="recall">
        <p class="stem">From memory: name the four hops from CLI flag to Redis.</p>
        <div class="answer">1. … 2. … 3. … 4. …</div>
      </div>

   3. CLOZE — fill the blank in a path, command, or type name.

      <div class="cloze" data-answer="internal/bootstrap">
        <p class="stem">The embedded fleet-db subprocess is spawned from
        <input> /manager.go.</p>
        <div class="why">…</div>
      </div>

      Matching is case-insensitive and ignores surrounding whitespace and a
      trailing slash. Accept alternatives with data-answer="a|b|c".
--------------------------------------------------------------------------- */

(function () {
  'use strict';

  var STYLE = [
    '.quiz,.recall,.cloze{font-family:var(--sans);font-size:.92rem;margin:1.6rem 0}',
    '.quiz .q,.recall,.cloze{border:1px solid var(--rule);border-radius:6px;padding:1rem 1.2rem;margin:0 0 1rem;background:var(--paper)}',
    '.quiz .stem,.recall .stem,.cloze .stem{font-family:var(--serif);font-size:1rem;margin:0 0 .8rem;font-weight:600}',
    '.quiz .opts{list-style:none;padding:0;margin:0}',
    '.quiz .opts li{margin:0 0 .4rem;padding:.55rem .8rem;border:1px solid var(--rule);border-radius:4px;cursor:pointer;transition:background .12s,border-color .12s;font-family:var(--mono);font-size:.82rem;line-height:1.45}',
    '.quiz .opts li:hover{background:var(--paper-sunk)}',
    '.quiz.done .opts li{cursor:default}',
    '.quiz .opts li.correct{border-color:var(--ok);background:var(--ok-soft)}',
    '.quiz .opts li.wrong{border-color:var(--warn);background:var(--warn-soft)}',
    '.quiz .opts li .mark{float:right;font-family:var(--sans);font-weight:700;font-size:.7rem;letter-spacing:.06em}',
    '.quiz .opts li.correct .mark{color:var(--ok)}',
    '.quiz .opts li.wrong .mark{color:var(--warn)}',
    '.why,.recall .answer{display:none;margin-top:.85rem;padding-top:.8rem;border-top:1px dashed var(--rule);color:var(--ink-soft);font-family:var(--serif);font-size:.95rem}',
    '.why.show,.recall .answer.show{display:block}',
    '.why code,.recall .answer code{font-size:.82em}',
    '.recall textarea{width:100%;min-height:5.5rem;font-family:var(--mono);font-size:.82rem;line-height:1.5;padding:.6rem .7rem;border:1px solid var(--rule);border-radius:4px;background:var(--paper-sunk);color:var(--ink);resize:vertical}',
    '.recall textarea:focus{outline:2px solid var(--accent);outline-offset:-1px}',
    '.cloze input{font-family:var(--mono);font-size:.85rem;padding:.2rem .45rem;border:1px solid var(--rule);border-radius:3px;background:var(--paper-sunk);color:var(--ink);min-width:14rem}',
    '.cloze input:focus{outline:2px solid var(--accent);outline-offset:-1px}',
    '.cloze input.correct{border-color:var(--ok);background:var(--ok-soft)}',
    '.cloze input.wrong{border-color:var(--warn);background:var(--warn-soft)}',
    '.qbtn{font-family:var(--sans);font-size:.76rem;font-weight:600;letter-spacing:.04em;padding:.4rem .85rem;margin:.7rem .4rem 0 0;border:1px solid var(--rule);border-radius:4px;background:var(--paper-sunk);color:var(--ink-soft);cursor:pointer}',
    '.qbtn:hover{border-color:var(--accent);color:var(--accent)}',
    '.qbtn.primary{background:var(--accent);border-color:var(--accent);color:var(--paper)}',
    '.qbtn.primary:hover{opacity:.88;color:var(--paper)}',
    '.grade{margin-top:.7rem;font-size:.8rem;color:var(--ink-faint)}',
    '.grade b{font-family:var(--sans);font-size:.7rem;letter-spacing:.08em;text-transform:uppercase;display:block;margin-bottom:.35rem}',
    '.quiz-score{font-family:var(--sans);font-size:.8rem;color:var(--ink-faint);border-top:1px solid var(--rule);padding-top:.7rem;margin-top:.2rem}',
    '.quiz-score b{color:var(--ink)}',
    '.revisit{color:var(--warn);font-weight:600}',
    '@media print{.quiz .opts li{border-color:#ccc}.why,.recall .answer{display:block}.qbtn,.recall textarea{display:none}}',
  ].join('');

  function injectStyles() {
    var el = document.createElement('style');
    el.textContent = STYLE;
    document.head.appendChild(el);
  }

  /* localStorage is best-effort: it can throw outright in a private window or
     when a browser blocks site data for file:// URLs. Never let it break a lesson. */
  function store(key, value) {
    try {
      if (value === undefined) return window.localStorage.getItem(key);
      window.localStorage.setItem(key, value);
    } catch (e) { /* no persistence available; the widgets still work */ }
    return null;
  }

  function button(label, cls) {
    var b = document.createElement('button');
    b.type = 'button';
    b.className = 'qbtn' + (cls ? ' ' + cls : '');
    b.textContent = label;
    return b;
  }

  /* --- multiple choice ---------------------------------------------------- */

  function wireQuiz(quiz) {
    var id = quiz.getAttribute('data-quiz') || 'q';
    var questions = [].slice.call(quiz.querySelectorAll('.q'));
    var answered = 0, right = 0;

    var score = document.createElement('div');
    score.className = 'quiz-score';
    quiz.appendChild(score);

    function paint() {
      if (!answered) { score.textContent = ''; return; }
      var msg = 'Answered <b>' + answered + '/' + questions.length + '</b> — <b>' + right + '</b> correct.';
      if (answered === questions.length && right < questions.length) {
        msg += ' <span class="revisit">Re-read the sections behind the ones you missed before moving on.</span>';
      } else if (answered === questions.length) {
        msg += ' Nothing to revisit — go on to the exercise.';
      }
      score.innerHTML = msg;
    }

    questions.forEach(function (q, qi) {
      var correct = parseInt(q.getAttribute('data-answer'), 10);
      var opts = [].slice.call(q.querySelectorAll('.opts li'));
      var why = q.querySelector('.why');
      var locked = false;

      opts.forEach(function (opt, oi) {
        opt.addEventListener('click', function () {
          if (locked) return;
          locked = true;
          answered++;
          var hit = oi === correct;
          if (hit) right++;

          opts.forEach(function (o, i) {
            if (i === correct) {
              o.classList.add('correct');
              o.insertAdjacentHTML('beforeend', '<span class="mark">correct</span>');
            } else if (i === oi) {
              o.classList.add('wrong');
              o.insertAdjacentHTML('beforeend', '<span class="mark">you</span>');
            }
          });
          if (why) why.classList.add('show');
          store('loom-quiz-' + id + '-' + qi, hit ? '1' : '0');
          paint();
        });
      });
    });
  }

  /* --- free recall --------------------------------------------------------- */

  function wireRecall(recall) {
    var answer = recall.querySelector('.answer');
    var ta = document.createElement('textarea');
    ta.setAttribute('placeholder', 'Write it from memory first. Nothing is graded — the effort of retrieving is the point.');
    recall.insertBefore(ta, answer);

    var reveal = button('Reveal the answer', 'primary');
    recall.insertBefore(reveal, answer);

    reveal.addEventListener('click', function () {
      answer.classList.add('show');
      reveal.remove();

      var grade = document.createElement('div');
      grade.className = 'grade';
      grade.innerHTML = '<b>Grade yourself honestly</b>';
      ['Got it', 'Partial', 'Blank'].forEach(function (label) {
        var b = button(label);
        b.addEventListener('click', function () {
          grade.innerHTML = '<b>Grade yourself honestly</b>' +
            (label === 'Got it'
              ? 'Got it — this one is moving into long-term storage. Come back to it next week anyway; spacing is what keeps it there.'
              : label === 'Partial'
                ? 'Partial — reread the section, then close the lesson and try writing it again cold in an hour.'
                : 'Blank — that is useful information, not a failure. Reread the section and redo this card at the start of your next session.');
        });
        grade.appendChild(b);
      });
      recall.appendChild(grade);
    });
  }

  /* --- cloze --------------------------------------------------------------- */

  function normalise(s) {
    return String(s).trim().toLowerCase().replace(/\/+$/, '').replace(/\s+/g, ' ');
  }

  function wireCloze(cloze) {
    var accepted = (cloze.getAttribute('data-answer') || '').split('|').map(normalise);
    var input = cloze.querySelector('input');
    var why = cloze.querySelector('.why');
    if (!input) return;

    var check = button('Check', 'primary');
    var checked = false;

    function judge() {
      if (checked) return;
      checked = true;
      var hit = accepted.indexOf(normalise(input.value)) !== -1;
      input.classList.add(hit ? 'correct' : 'wrong');
      input.setAttribute('readonly', 'readonly');
      if (!hit) {
        var truth = document.createElement('div');
        truth.className = 'grade';
        truth.innerHTML = '<b>Answer</b><code>' + accepted[0] + '</code>';
        cloze.insertBefore(truth, why);
      }
      if (why) why.classList.add('show');
      check.remove();
    }

    check.addEventListener('click', judge);
    input.addEventListener('keydown', function (e) { if (e.key === 'Enter') { e.preventDefault(); judge(); } });
    cloze.insertBefore(check, why);
  }

  /* --- boot ---------------------------------------------------------------- */

  function boot() {
    injectStyles();
    [].forEach.call(document.querySelectorAll('.quiz'), wireQuiz);
    [].forEach.call(document.querySelectorAll('.recall'), wireRecall);
    [].forEach.call(document.querySelectorAll('.cloze'), wireCloze);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
