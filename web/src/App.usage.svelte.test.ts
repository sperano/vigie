import { beforeEach, describe, expect, test } from 'vitest';
import { flushSync } from 'svelte';
import App from './App.svelte';
import type { Snapshot } from './lib/model';
import { render } from './render';
import { BOARD, TASK } from './testing/board';
import { row, snapshot, TEST_NOW } from './testing/builders';
import { FakeApi, FakeStreams, FakeTimers, settle } from './testing/fakes';

let api: FakeApi;
let streams: FakeStreams;
let page: HTMLElement;

beforeEach(() => {
	api = new FakeApi();
	streams = new FakeStreams();
});

async function mountWith(first: Snapshot): Promise<void> {
	page = render(App, {
		api,
		openStream: streams.open,
		timer: new FakeTimers().timer,
		now: () => TEST_NOW,
		openUrl: () => true
	});
	api.answer(first);
	await settle();
	streams.last.open();
	flushSync();
}

const CLAUDE = row({
	source: 'usage',
	name: 'claude-oauth-secondary',
	origin: 'kelos',
	state: 'ok',
	age: '3m',
	detail: '[active] 7d 8% resets 5d',
	target: 'https://claude.ai/settings/usage'
});
const CODEX = row({
	source: 'usage',
	name: 'codex-oauth',
	origin: 'kelos',
	mark: '●',
	state: 'limited',
	detail: '[prolite] 5h 100% resets 2h',
	severity: 'blocked',
	needsYou: true
});

const accounts = () => [...page.querySelectorAll<HTMLLIElement>('header .usage .account')];
const text = (el: Element) => (el.textContent ?? '').replace(/\s+/g, ' ').trim();

describe('the account usage', () => {
	test('is drawn in the app bar, not as rows', async () => {
		await mountWith(snapshot([TASK], { usage: [CLAUDE, CODEX] }));
		expect(accounts().map(text)).toEqual([
			'claude-oauth-secondary ok [active] 7d 8% resets 5d',
			'● codex-oauth limited [prolite] 5h 100% resets 2h'
		]);
		const severities = ['quiet', 'blocked'];
		expect(accounts().map((li) => severities.filter((sev) => li.classList.contains(sev)))).toEqual([
			['quiet'],
			['blocked']
		]);
		expect([...page.querySelectorAll('tbody tr .name')].map(text)).toEqual(['#7 - Vigie #2']);
	});

	test('links an account to its usage page when it has one', async () => {
		await mountWith(snapshot([TASK], { usage: [CLAUDE, CODEX] }));
		const [claude, codex] = accounts();
		const link = claude?.querySelector('a');
		expect(link?.getAttribute('href')).toBe('https://claude.ai/settings/usage');
		expect(link?.getAttribute('target')).toBe('_blank');
		expect(link?.getAttribute('rel')).toBe('noopener noreferrer');
		expect(claude?.getAttribute('title')).toBe('read 3m ago');
		expect(codex?.querySelector('a')).toBeNull();
		expect(codex?.getAttribute('title')).toBeNull();
	});

	test('leaves the app bar without a usage list when there is none', async () => {
		await mountWith(BOARD);
		expect(page.querySelector('header .usage')).toBeNull();
	});
});
