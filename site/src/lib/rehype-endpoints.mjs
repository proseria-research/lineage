/**
 * Renders API endpoint tables as endpoint lists.
 *
 * Any Markdown table whose first two header cells read "Method" and "Path" becomes a list:
 * each row is a method badge and the full path on one line, with `{params}` highlighted, and
 * the remaining cells (purpose, notes, returns) underneath. Guides keep writing plain Markdown
 * tables; long paths no longer crush the description into a thin column.
 */

const textOf = (node) =>
	node.type === 'text' ? node.value : (node.children ?? []).map(textOf).join('');

const el = (tagName, className, children, extra = {}) => ({
	type: 'element',
	tagName,
	properties: { className: className ? [className] : undefined, ...extra },
	children,
});

const rows = (section) =>
	(section?.children ?? []).filter((n) => n.type === 'element' && n.tagName === 'tr');
const cells = (tr) =>
	tr.children.filter((n) => n.type === 'element' && (n.tagName === 'td' || n.tagName === 'th'));
const child = (node, tag) => node.children?.find((n) => n.type === 'element' && n.tagName === tag);

/** The path as text, with every `{param}` wrapped so it can be tinted. */
const pathNodes = (text) =>
	text
		.split(/(\{[^}]+\})/)
		.filter(Boolean)
		.map((part) =>
			part.startsWith('{') ? el('span', 'endpoint__param', [{ type: 'text', value: part }]) : { type: 'text', value: part },
		);

function transform(table) {
	const head = child(table, 'thead');
	const body = child(table, 'tbody');
	const header = rows(head)[0];
	if (!header || !body) return null;
	const names = cells(header).map((c) => textOf(c).trim().toLowerCase());
	if (names[0] !== 'method' || names[1] !== 'path') return null;

	const items = rows(body).map((tr) => {
		const [method, path, ...rest] = cells(tr);
		const verb = textOf(method).trim();
		const line = el('div', 'endpoint__line', [
			el('span', 'endpoint__method', [{ type: 'text', value: verb }], { dataMethod: verb.toLowerCase() }),
			el('code', 'endpoint__path', pathNodes(textOf(path).trim())),
		]);
		const notes = rest.filter((c) => textOf(c).trim() !== '').map((c) => el('div', 'endpoint__desc', c.children));
		return el('li', 'endpoint', [line, ...notes]);
	});
	return el('ul', 'endpoints', items);
}

function walk(node) {
	if (!node.children) return;
	node.children = node.children.map((c) => {
		if (c.type === 'element' && c.tagName === 'table') return transform(c) ?? c;
		walk(c);
		return c;
	});
}

export default function rehypeEndpoints() {
	return (tree) => walk(tree);
}
