'use strict';
// Highlight the active sidebar nav link as the user scrolls the user guide.
// External file (not inline) so the Caddy CSP can stay script-src 'self'.
const sections = document.querySelectorAll('section[id]');
const navLinks = document.querySelectorAll('nav.sidebar a');
const io = new IntersectionObserver(entries => {
  entries.forEach(e => {
    if (!e.isIntersecting) return;
    navLinks.forEach(a => a.classList.remove('active'));
    const link = document.querySelector('nav.sidebar a[href="#' + e.target.id + '"]');
    if (link) link.classList.add('active');
  });
}, { rootMargin: '-15% 0px -75% 0px' });
sections.forEach(s => io.observe(s));
