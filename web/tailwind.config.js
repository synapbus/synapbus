/** @type {import('tailwindcss').Config} */

function withOpacityValue(variable) {
	return ({ opacityValue }) => {
		if (opacityValue !== undefined) {
			return `rgba(var(${variable}), ${opacityValue})`;
		}
		return `rgb(var(${variable}))`;
	};
}

export default {
	content: ['./src/**/*.{html,js,svelte,ts}'],
	theme: {
		extend: {
			colors: {
				'bg-primary': '#1a1d21',
				'bg-secondary': '#222529',
				'bg-tertiary': '#2c2f33',
				'bg-input': '#383b40',
				'text-primary': '#e8e8e8',
				'text-secondary': '#9b9da0',
				'text-link': '#1d9bd1',
				'accent-green': '#2eb67d',
				'accent-yellow': '#ecb22e',
				'accent-red': '#e01e5a',
				'accent-blue': '#36c5f0',
				'accent-purple': '#7c3aed',
				'border': '#383b40',
				'border-active': '#545760'
			},
			fontFamily: {
				display: ['Instrument Sans', 'sans-serif'],
				body: ['DM Sans', 'sans-serif'],
				mono: ['JetBrains Mono', 'monospace']
			}
		}
	},
	plugins: []
};
