import type { JSX } from "preact";

// A candidate is a run of non-space characters that starts with a scheme and "://". Only candidates whose
// parsed protocol is http: or https: become links; every other candidate stays text.
const candidate = /[A-Za-z][A-Za-z0-9+.-]*:\/\/\S*/g;
const trailingPunctuation = /[.,;:!?)\]}'"]+$/;

export function ReplyText({ text }: { text: string }) {
  const children: (string | JSX.Element)[] = [];
  let kept = 0;
  for (const match of text.matchAll(candidate)) {
    const start = match.index;
    const url = match[0].replace(trailingPunctuation, "");
    if (!isWebURL(url)) {
      continue;
    }
    if (start > kept) {
      children.push(text.slice(kept, start));
    }
    children.push(
      <a href={url} rel="noopener noreferrer" target="_blank">
        {url}
      </a>,
    );
    kept = start + url.length;
  }
  if (kept < text.length) {
    children.push(text.slice(kept));
  }
  return <span class="reply">{children}</span>;
}

function isWebURL(candidate: string): boolean {
  try {
    const { protocol } = new URL(candidate);
    return protocol === "http:" || protocol === "https:";
  } catch {
    return false;
  }
}
