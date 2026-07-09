import createDOMPurify from 'dompurify';
import hljs from 'highlight.js/lib/core';
import ini from 'highlight.js/lib/languages/ini';
import json from 'highlight.js/lib/languages/json';
import plaintext from 'highlight.js/lib/languages/plaintext';
import xml from 'highlight.js/lib/languages/xml';
import yaml from 'highlight.js/lib/languages/yaml';
import MarkdownIt from 'markdown-it';
import { fileExtension } from '../../modules/media-types';

export type TextViewerMode = 'plain' | 'markdown' | 'code';
export type StructuredTextLanguage = 'ini' | 'json' | 'plaintext' | 'xml' | 'yaml';

interface StructuredTextRenderInput {
    mode: Exclude<TextViewerMode, 'plain'>;
    source: string;
    language?: StructuredTextLanguage;
}

const MARKDOWN_EXTENSIONS = new Set(['md', 'markdown']);
const CODE_LANGUAGES = new Map<string, StructuredTextLanguage>([
    ['cfg', 'ini'],
    ['conf', 'ini'],
    ['ini', 'ini'],
    ['json', 'json'],
    ['yaml', 'yaml'],
    ['yml', 'yaml'],
    ['toml', 'ini'],
    ['xml', 'xml'],
]);
const STRUCTURED_ALLOWED_TAGS = new Set([
    'a',
    'blockquote',
    'br',
    'code',
    'del',
    'em',
    'h1',
    'h2',
    'h3',
    'h4',
    'h5',
    'h6',
    'hr',
    'li',
    'ol',
    'p',
    'pre',
    'span',
    'strong',
    'table',
    'tbody',
    'td',
    'th',
    'thead',
    'tr',
    'ul',
]);
const DROP_CONTENT_TAGS = new Set(['script', 'style', 'iframe', 'object', 'embed', 'template']);
const HLJS_CLASS = /^(?:hljs(?:-[\w-]+)?|language-[\w-]+)$/;

hljs.registerLanguage('ini', ini);
hljs.registerLanguage('json', json);
hljs.registerLanguage('plaintext', plaintext);
hljs.registerLanguage('xml', xml);
hljs.registerLanguage('yaml', yaml);

const markdown = new MarkdownIt({
    html: false,
    linkify: true,
    typographer: false,
});

type MarkdownRenderRule = (
    tokens: Array<{ attrIndex(name: string): number; attrs?: Array<[string, string]>; attrSet(name: string, value: string): void }>,
    idx: number,
    options: unknown,
    env: unknown,
    self: { renderToken(tokens: unknown, idx: number, options: unknown): string },
) => string;

const defaultLinkOpen: MarkdownRenderRule =
    markdown.renderer.rules.link_open ??
    ((tokens, idx, options, _env, self) => self.renderToken(tokens, idx, options));

const secureLinkOpen: MarkdownRenderRule = (tokens, idx, options, env, self) => {
    const hrefIndex = tokens[idx].attrIndex('href');
    const href = hrefIndex >= 0 ? tokens[idx].attrs?.[hrefIndex]?.[1] ?? '' : '';
    if (/^(https?:)?\/\//i.test(href)) {
        tokens[idx].attrSet('target', '_blank');
        tokens[idx].attrSet('rel', 'noreferrer noopener');
    }
    return defaultLinkOpen(tokens, idx, options, env, self);
};

markdown.renderer.rules.link_open = secureLinkOpen;

export function textViewerModeForName(name: string): TextViewerMode {
    const ext = fileExtension(name);
    if (MARKDOWN_EXTENSIONS.has(ext)) return 'markdown';
    if (CODE_LANGUAGES.has(ext)) return 'code';
    return 'plain';
}

export function structuredTextLanguageForName(name: string): StructuredTextLanguage | null {
    return CODE_LANGUAGES.get(fileExtension(name)) ?? null;
}

export function renderStructuredText({ mode, source, language = 'plaintext' }: StructuredTextRenderInput): string {
    const html = mode === 'markdown' ? markdown.render(source) : renderCodeBlock(source, language);
    return sanitizeStructuredHtml(html);
}

function renderCodeBlock(source: string, language: StructuredTextLanguage): string {
    const highlighted = hljs.highlight(source, {
        language,
        ignoreIllegals: true,
    }).value;
    return `<pre class="text-code-block"><code class="hljs language-${language}">${highlighted}</code></pre>`;
}

function sanitizeStructuredHtml(html: string): string {
    if (typeof window === 'undefined') {
        return html
            .replace(/<script[\s\S]*?>[\s\S]*?<\/script>/gi, '')
            .replace(/\son\w+=(["']).*?\1/gi, '')
            .replace(/\s(href|src)=(["'])javascript:[\s\S]*?\2/gi, '');
    }
    let sanitized = html;
    // DOMPurify explicitly does not consider happy-dom a safe DOM implementation.
    // Use it in real browsers, then finish with our renderer-specific allowlist.
    if (!('happyDOM' in window)) {
        const DOMPurify = createDOMPurify(window);
        sanitized = DOMPurify.sanitize(html, {
            USE_PROFILES: { html: true },
            FORBID_TAGS: ['style', 'script'],
            FORBID_ATTR: ['style', 'onerror', 'onclick'],
            ALLOW_DATA_ATTR: false,
            RETURN_TRUSTED_TYPE: false,
        });
    }
    return normalizeStructuredHtml(sanitized);
}

function normalizeStructuredHtml(html: string): string {
    const parsed = new window.DOMParser().parseFromString(html, 'text/html');
    const out = document.implementation.createHTMLDocument('');
    const host = out.createElement('div');
    copySanitizedChildren(parsed.body, host, out);
    return host.innerHTML;
}

function copySanitizedChildren(sourceParent: ParentNode, targetParent: ParentNode, doc: Document): void {
    for (const child of Array.from(sourceParent.childNodes)) {
        if (child.nodeType === Node.TEXT_NODE) {
            targetParent.append(doc.createTextNode(child.textContent ?? ''));
            continue;
        }
        if (child.nodeType !== Node.ELEMENT_NODE) continue;
        copySanitizedElement(child as Element, targetParent, doc);
    }
}

function copySanitizedElement(source: Element, targetParent: ParentNode, doc: Document): void {
    const tag = source.tagName.toLowerCase();
    if (DROP_CONTENT_TAGS.has(tag)) return;

    if (!STRUCTURED_ALLOWED_TAGS.has(tag)) {
        copySanitizedChildren(source, targetParent, doc);
        return;
    }

    const target = doc.createElement(tag);
    applyAllowedAttributes(source, target);
    copySanitizedChildren(source, target, doc);
    targetParent.append(target);
}

function applyAllowedAttributes(source: Element, target: Element): void {
    if (source.hasAttribute('class')) {
        const className = sanitizeStructuredClassName(source.getAttribute('class') ?? '');
        if (className) target.setAttribute('class', className);
    }

    if (target.tagName.toLowerCase() !== 'a') return;
    const href = sanitizeStructuredHref(source.getAttribute('href') ?? '');
    if (!href) return;
    target.setAttribute('href', href);
    if (/^(?:https?:|mailto:)/i.test(href)) {
        target.setAttribute('target', '_blank');
        target.setAttribute('rel', 'noreferrer noopener');
    }
}

function sanitizeStructuredClassName(value: string): string {
    return value
        .split(/\s+/)
        .map((token) => token.trim())
        .filter((token) => token.length > 0 && HLJS_CLASS.test(token))
        .join(' ');
}

function sanitizeStructuredHref(value: string): string {
    const href = value.trim();
    if (!href) return '';
    if (href.startsWith('#') || href.startsWith('/') || href.startsWith('./') || href.startsWith('../')) return href;
    if (/^(?:https?:|mailto:)/i.test(href)) return href;
    return '';
}
