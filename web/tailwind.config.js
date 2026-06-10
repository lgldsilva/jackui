/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  // hover: variants apply ONLY on devices that truly support hover (mouse).
  // Without this, iOS Safari needs two taps on any element with a hover style:
  // the first applies the sticky :hover, the second fires the click — exactly
  // the "tap twice to play a file" bug in the torrent contents modal.
  future: {
    hoverOnlyWhenSupported: true,
  },
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        // Semantic theme tokens — driven by CSS variables in index.css. Using
        // the bare token (e.g. bg-surface) lets the .dark class on <html> swap
        // both palettes without rewriting components.
        surface: 'var(--bg-primary)',
        'surface-secondary': 'var(--bg-secondary)',
        'surface-tertiary': 'var(--bg-tertiary)',
        'surface-elevated': 'var(--bg-elevated)',
        'text-primary': 'var(--text-primary)',
        'text-secondary': 'var(--text-secondary)',
        'text-muted': 'var(--text-muted)',
        // `default` is the border-color alias — needed here so that Tailwind
        // generates `divide-default` (divide-{color} reads from theme('colors'),
        // not from borderColor). As a side effect it also enables `bg-default`
        // and `text-default` but those are never used.
        default: 'var(--border-color)',
        // Border color is set via extend.borderColor below (DEFAULT + .strong).
        // The `border` utility (border-width:1px) is built-in and uses whichever
        // borderColor.DEFAULT resolves to. `border-default` also works for
        // consistency with the other .-default tokens.
        input: 'var(--input-bg)',
        card: 'var(--card-bg)',
        // Legacy gray ramp kept around for components that still need a
        // mid-gray divider or chip background. They don't follow the theme
        // tokens — they were defined before the theme work. New code should
        // prefer the semantic tokens above.
        gray: {
          750: '#2d3748',
          850: '#1a202c',
          950: '#0d1117',
        }
      },
      borderColor: {
        DEFAULT: 'var(--border-color)',
        default: 'var(--border-color)',
        strong: 'var(--border-strong)',
      },
      divideColor: {
        DEFAULT: 'var(--border-color)',
        default: 'var(--border-color)',
        strong: 'var(--border-strong)',
      },
      backgroundColor: {
        'hover-overlay': 'var(--hover-overlay)',
      },
      boxShadow: {
        card: 'var(--shadow-card)',
        elevated: 'var(--shadow-elevated)',
      },
      ringColor: {
        // Brand-green focus ring — green-600 in light (enough contrast on the
        // white inputs), green-500 in dark. Defined per-theme in index.css.
        focus: 'var(--focus-ring)',
      },
    },
  },
  plugins: [],
}
