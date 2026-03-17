import { writable } from 'svelte/store';
import { browser } from '$app/environment';

const MIN_SIZE = 12;
const MAX_SIZE = 24;
const STEP = 2;
const DEFAULT_SIZE = 16;
const STORAGE_KEY = 'synapbus-font-size';

function getInitialSize(): number {
	if (!browser) return DEFAULT_SIZE;
	const stored = localStorage.getItem(STORAGE_KEY);
	if (stored) {
		const val = parseInt(stored, 10);
		if (!isNaN(val) && val >= MIN_SIZE && val <= MAX_SIZE) return val;
	}
	return DEFAULT_SIZE;
}

export const fontSize = writable<number>(getInitialSize());

export function applyFontSize(size: number) {
	if (browser) {
		document.documentElement.style.fontSize = `${size}px`;
		localStorage.setItem(STORAGE_KEY, String(size));
	}
}

export function increaseFontSize() {
	fontSize.update((s) => {
		const next = Math.min(s + STEP, MAX_SIZE);
		applyFontSize(next);
		return next;
	});
}

export function decreaseFontSize() {
	fontSize.update((s) => {
		const next = Math.max(s - STEP, MIN_SIZE);
		applyFontSize(next);
		return next;
	});
}

export { MIN_SIZE, MAX_SIZE };
