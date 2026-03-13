import { writable } from 'svelte/store';
import { browser } from '$app/environment';

function getInitialTheme(): 'light' | 'dark' {
	if (!browser) return 'light';
	const stored = localStorage.getItem('synapbus-theme');
	if (stored === 'dark') return 'dark';
	if (stored === 'light') return 'light';
	return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

export const theme = writable<'light' | 'dark'>(getInitialTheme());

export function toggleTheme() {
	theme.update((t) => {
		const next = t === 'light' ? 'dark' : 'light';
		if (browser) {
			localStorage.setItem('synapbus-theme', next);
			if (next === 'dark') {
				document.documentElement.classList.add('dark');
			} else {
				document.documentElement.classList.remove('dark');
			}
		}
		return next;
	});
}
