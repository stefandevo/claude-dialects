// Scroll reveal
const revealEls = document.querySelectorAll('.reveal');
const revealObserver = new IntersectionObserver(
  (entries) => {
    entries.forEach((entry) => {
      if (entry.isIntersecting) {
        entry.target.classList.add('visible');
      }
    });
  },
  { threshold: 0.12, rootMargin: '0px 0px -40px 0px' }
);
revealEls.forEach((el) => revealObserver.observe(el));

// Config tabs (index page)
const tabs = document.querySelectorAll('.config-tab');
const panels = document.querySelectorAll('.config-panel');

tabs.forEach((tab) => {
  tab.addEventListener('click', () => {
    const target = tab.dataset.tab;
    tabs.forEach((t) => t.classList.remove('active'));
    panels.forEach((p) => p.classList.remove('active'));
    tab.classList.add('active');
    document.querySelector(`[data-panel="${target}"]`)?.classList.add('active');
  });
});

// Copy buttons — find code by id from data-copy attribute
document.querySelectorAll('.copy-btn').forEach((btn) => {
  btn.addEventListener('click', async () => {
    const key = btn.dataset.copy;
    const el = document.getElementById(`code-${key}`);
    if (!el) return;

    try {
      await navigator.clipboard.writeText(el.textContent.trim());
      btn.textContent = 'Copied!';
      btn.classList.add('copied');
      setTimeout(() => {
        btn.textContent = 'Copy';
        btn.classList.remove('copied');
      }, 2000);
    } catch {
      btn.textContent = 'Failed';
      setTimeout(() => { btn.textContent = 'Copy'; }, 2000);
    }
  });
});

// Nav background on scroll
const nav = document.querySelector('.nav');
window.addEventListener('scroll', () => {
  if (!nav) return;
  nav.style.background = window.scrollY > 80
    ? 'rgba(7, 8, 15, 0.92)'
    : 'rgba(7, 8, 15, 0.65)';
}, { passive: true });

// Parallax on aurora blobs
const blobs = document.querySelectorAll('.aurora__blob');
if (blobs.length && !window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
  window.addEventListener('mousemove', (e) => {
    const x = (e.clientX / window.innerWidth - 0.5) * 20;
    const y = (e.clientY / window.innerHeight - 0.5) * 20;
    blobs.forEach((blob, i) => {
      const factor = (i + 1) * 0.5;
      blob.style.transform = `translate(${x * factor}px, ${y * factor}px)`;
    });
  }, { passive: true });
}

// Mobile nav toggle
const navToggle = document.querySelector('.nav__toggle');
const mobileNav = document.getElementById('mobile-nav');

if (navToggle && mobileNav) {
  navToggle.addEventListener('click', () => {
    const open = mobileNav.classList.toggle('open');
    navToggle.setAttribute('aria-expanded', open);
    navToggle.setAttribute('aria-label', open ? 'Close menu' : 'Open menu');
  });

  mobileNav.querySelectorAll('a').forEach((link) => {
    link.addEventListener('click', () => {
      mobileNav.classList.remove('open');
      navToggle.setAttribute('aria-expanded', 'false');
    });
  });
}

// Doc sidebar — highlight active section on scroll
const docToc = document.getElementById('doc-toc');
if (docToc) {
  const tocLinks = docToc.querySelectorAll('a[href^="#"]');
  const sections = [];

  tocLinks.forEach((link) => {
    const id = link.getAttribute('href').slice(1);
    const section = document.getElementById(id);
    if (section) sections.push({ link, section });
  });

  if (sections.length) {
    const tocObserver = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting) {
            const match = sections.find((s) => s.section === entry.target);
            if (match) {
              tocLinks.forEach((l) => l.classList.remove('active'));
              match.link.classList.add('active');
            }
          }
        });
      },
      { rootMargin: '-20% 0px -60% 0px', threshold: 0 }
    );

    sections.forEach(({ section }) => tocObserver.observe(section));
  }
}
