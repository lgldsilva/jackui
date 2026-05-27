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
        gray: {
          750: '#2d3748',
          850: '#1a202c',
          950: '#0d1117',
        }
      }
    },
  },
  plugins: [],
}
