// Turns `<a class="markup-embed">` links (from the `$[name](link)` markdown syntax,
// only emitted in documents such as READMEs) into sandboxed iframes.

// Enough for video players and web games. Never add allow-top-navigation*
// (the embed could navigate this page away) or allow-modals.
export const EMBED_SANDBOX = [
  'allow-scripts',
  'allow-same-origin', // the embed's own origin; ours is refused in isEmbeddableUrl
  'allow-forms',
  'allow-popups',
  'allow-popups-to-escape-sandbox',
  'allow-pointer-lock',
  'allow-presentation',
].join(' ');

export const EMBED_ALLOW = 'autoplay; encrypted-media; fullscreen; picture-in-picture; gamepad';

function originOf(url) {
  try {
    return new URL(url).origin;
  } catch {
    return null;
  }
}

export function isEmbeddableUrl(href) {
  let url;
  try {
    url = new URL(href);
  } catch {
    return false;
  }
  const allowedProtocols = window.location.protocol === 'http:' ? ['https:', 'http:'] : ['https:'];
  if (!allowedProtocols.includes(url.protocol) || url.username || url.password) return false;
  // A same-origin page with allow-scripts + allow-same-origin could remove its own sandbox.
  const ownOrigins = [window.location.origin, originOf(window.config.appUrl)];
  return !ownOrigins.includes(url.origin);
}

export function createEmbed(link) {
  const iframe = document.createElement('iframe');
  // all restrictions must be in place before src is set
  iframe.setAttribute('sandbox', EMBED_SANDBOX);
  iframe.setAttribute('allow', EMBED_ALLOW);
  iframe.setAttribute('allowfullscreen', '');
  iframe.setAttribute('credentialless', ''); // no third-party cookies, ephemeral storage (Chromium)
  iframe.setAttribute('referrerpolicy', 'strict-origin');
  iframe.setAttribute('loading', 'lazy');
  iframe.title = link.textContent.trim();
  iframe.src = link.href;

  // a span, since the link usually sits inside a <p>
  const wrapper = document.createElement('span');
  wrapper.className = 'markup-embed-frame';
  wrapper.append(iframe);
  return wrapper;
}

export function renderEmbed() {
  for (const link of document.querySelectorAll('.markup a.markup-embed')) {
    link.classList.remove('markup-embed'); // process each link only once
    if (!isEmbeddableUrl(link.getAttribute('href'))) continue;
    link.replaceWith(createEmbed(link));
  }
}
