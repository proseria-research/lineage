// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import rehypeEndpoints from './src/lib/rehype-endpoints.mjs';

// TODO: point this at the real domain before the first production deploy.
// It is used for canonical URLs, Open Graph tags, and the sitemap.
const site = 'https://lineage.proseria.ca';

const repo = 'https://github.com/proseria-research/lineage';

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
				'A model registry you run yourself: what is live, what changed, where it came from and who approved it. One Go binary.',
			logo: {
				light: './src/assets/mark-light.svg',
				dark: './src/assets/mark-dark.svg',
			},
			favicon: '/favicon.svg',
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
	],
});
