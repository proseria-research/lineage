// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import rehypeEndpoints from './src/lib/rehype-endpoints.mjs';
import markdownExport from 'astro-md-content-negotiation';

// Used for canonical URLs, Open Graph tags, and the sitemap.
const site = 'https://lineage.proseria.ca';

const repo = 'https://github.com/proseria-research/lineage';

// Left out of the Markdown copies: page chrome, copy buttons, and the home page's interactive
// demos (their prose stays). Diagrams are SVG, which the plugin already drops.
const notForAgents = [
	'[aria-hidden="true"]',
	'.sr-only',
	'.sl-anchor-link',
	'.copy',
	'button[data-copy]',
	'[role="tablist"]',
	'figure.demo',
	'figure.compare',
	'figure.layers',
	'.attention',
].join(',');

export default defineConfig({
	site,
	// Fully static output — the build is a directory of files that Cloudflare
	// serves directly. No adapter, no server runtime, no cold starts.
	output: 'static',
	// Method/Path tables in the guides render as endpoint lists (src/lib/rehype-endpoints.mjs).
	markdown: { rehypePlugins: [rehypeEndpoints] },
	integrations: [
		starlight({
			title: 'Lineage',
			description:
				'The self-hostable system of record and model registry for ML/AI models.',
			logo: {
				light: './src/assets/mark-light.svg',
				dark: './src/assets/mark-dark.svg',
			},
			favicon: '/favicon.svg',
			// Social preview card for every guide page (Starlight sets the other OG tags).
			head: [
				{ tag: 'meta', attrs: { property: 'og:image', content: `${site}/og.png` } },
				{ tag: 'meta', attrs: { property: 'og:image:width', content: '1200' } },
				{ tag: 'meta', attrs: { property: 'og:image:height', content: '630' } },
				{ tag: 'meta', attrs: { name: 'twitter:image', content: `${site}/og.png` } },
			],
			customCss: [
				'./src/styles/fonts.css',
				'./src/styles/tokens.css',
				'./src/styles/docs.css',
			],
			social: [{ icon: 'github', label: 'GitHub', href: repo }],
			editLink: { baseUrl: `${repo}/edit/main/site/` },
			lastUpdated: true,
			pagination: true,
			credits: false,
			tableOfContents: { minHeadingLevel: 2, maxHeadingLevel: 3 },
			// Higher-contrast code than the default theme, framed like the console's cards.
			expressiveCode: {
				themes: ['github-dark', 'github-light'],
				styleOverrides: {
					borderRadius: '8px',
					borderColor: 'var(--border)',
					codeFontSize: '0.85rem',
					codeBackground: 'var(--muted)',
					frames: {
						editorTabBarBackground: 'var(--muted)',
						terminalTitlebarBackground: 'var(--muted)',
						terminalBackground: 'var(--muted)',
						frameBoxShadowCssValue: 'none',
					},
				},
			},
			sidebar: [
				{ label: 'Start here', items: [{ autogenerate: { directory: 'start' } }] },
				{ label: 'Model API', items: [{ autogenerate: { directory: 'api' } }] },
				{ label: 'Governance', items: [{ autogenerate: { directory: 'governance' } }] },
				{ label: 'Operate', items: [{ autogenerate: { directory: 'operate' } }] },
				{ label: 'Clients', items: [{ autogenerate: { directory: 'clients' } }] },
			],
		}),
		// An `index.md` beside every page, served for `Accept: text/markdown` (public/_headers).
		markdownExport({
			exclude: ['404.html'],
			removeElements: [(node) => node.matches(notForAgents)],
			// The plugin's link rule outranks removeElements, so heading anchors survive as a bare `#id`.
			transform: (md) => md.replace(/^#[\w-]+\n\n/gm, ''),
		}),
	],
});
