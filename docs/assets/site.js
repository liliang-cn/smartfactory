// Copy the install command, open the live demo in place, remember the language picked.
(() => {
  const store = {
    get(k) { try { return localStorage.getItem(k); } catch { return null; } },
    set(k, v) { try { localStorage.setItem(k, v); } catch { /* private window */ } },
  };

  // the English page sends Chinese readers to theirs, once, unless they chose otherwise
  const lang = document.documentElement.lang;
  if (lang === 'en' && !store.get('sf-lang') && (navigator.languages || [navigator.language]).some((l) => /^zh\b/i.test(l))) {
    location.replace('zh/' + location.hash);
    return;
  }
  for (const a of document.querySelectorAll('.lang a')) a.addEventListener('click', () => store.set('sf-lang', a.hreflang));

  for (const b of document.querySelectorAll('.cmd')) {
    b.addEventListener('click', async () => {
      const label = b.querySelector('.copy');
      const was = label.textContent;
      try {
        await navigator.clipboard.writeText(b.dataset.copy);
        b.classList.add('done');
        label.textContent = b.dataset.done;
        setTimeout(() => { b.classList.remove('done'); label.textContent = was; }, 1800);
      } catch { /* no clipboard: the text is there to select */ }
    });
  }

  // the demo is a full 3D scene: on a wide screen it opens in place, on a phone in its own tab
  const stage = document.querySelector('.stage');
  const play = stage && stage.querySelector('.play');
  if (play) {
    play.addEventListener('click', (e) => {
      if (matchMedia('(max-width: 760px)').matches) return; // the link opens the demo
      e.preventDefault();
      const f = document.createElement('iframe');
      f.src = play.href;
      f.title = play.dataset.title;
      f.allow = 'fullscreen';
      stage.appendChild(f);
      stage.classList.add('live');
    });
  }

  const line = document.querySelector('.line');
  if (line && 'IntersectionObserver' in window) {
    new IntersectionObserver(([e]) => line.classList.toggle('moving', e.isIntersecting), { threshold: 0.05 }).observe(line);
  }
})();
