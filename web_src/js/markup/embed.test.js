import {EMBED_ALLOW, EMBED_SANDBOX, isEmbeddableUrl, renderEmbed} from './embed.js';

function setup(html) {
  document.body.innerHTML = `<div class="markup"><p>${html}</p></div>`;
}

beforeEach(() => {
  window.config.appUrl = 'https://forge.example/';
});

test('isEmbeddableUrl', () => {
  expect(isEmbeddableUrl('https://www.youtube.com/embed/abc')).toBe(true);
  expect(isEmbeddableUrl('https://game.example/play?x=1')).toBe(true);

  expect(isEmbeddableUrl('https://forge.example/user/repo/raw/a.html')).toBe(false);
  expect(isEmbeddableUrl(`${window.location.origin}/a.html`)).toBe(false);
  expect(isEmbeddableUrl('/relative')).toBe(false);
  expect(isEmbeddableUrl('javascript:alert(1)')).toBe(false); // eslint-disable-line no-script-url
  expect(isEmbeddableUrl('data:text/html,<script>alert(1)</script>')).toBe(false);
  expect(isEmbeddableUrl('https://user:pass@game.example/')).toBe(false);
  expect(isEmbeddableUrl('not a url')).toBe(false);
});

test('renderEmbed creates a sandboxed iframe', () => {
  setup('<a class="markup-embed" href="https://www.youtube.com/embed/abc">My video</a>');
  renderEmbed();

  const iframe = document.querySelector('.markup .markup-embed-frame > iframe');
  expect(iframe).not.toBeNull();
  expect(iframe.getAttribute('src')).toBe('https://www.youtube.com/embed/abc');
  expect(iframe.getAttribute('sandbox')).toBe(EMBED_SANDBOX);
  expect(EMBED_SANDBOX).not.toContain('top-navigation');
  expect(EMBED_SANDBOX).not.toContain('allow-modals');
  expect(iframe.getAttribute('allow')).toBe(EMBED_ALLOW);
  expect(iframe.hasAttribute('credentialless')).toBe(true);
  expect(iframe.getAttribute('referrerpolicy')).toBe('strict-origin');
  expect(iframe.getAttribute('loading')).toBe('lazy');
  expect(iframe.title).toBe('My video');
  expect(document.querySelector('a')).toBeNull();
});

test('renderEmbed keeps invalid links as links', () => {
  setup('<a class="markup-embed" href="https://forge.example/x">x</a>');
  renderEmbed();
  expect(document.querySelector('iframe')).toBeNull();
  expect(document.querySelector('a').getAttribute('href')).toBe('https://forge.example/x');
});

test('renderEmbed ignores links outside markup and plain links', () => {
  document.body.innerHTML = '<a class="markup-embed" href="https://game.example/">x</a><div class="markup"><a href="https://game.example/">y</a></div>';
  renderEmbed();
  expect(document.querySelector('iframe')).toBeNull();
});

test('renderEmbed is idempotent', () => {
  setup('<a class="markup-embed" href="https://game.example/">x</a>');
  renderEmbed();
  renderEmbed();
  expect(document.querySelectorAll('iframe')).toHaveLength(1);
});
