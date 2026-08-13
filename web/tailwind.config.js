import typography from '@tailwindcss/typography'

/**
 * Rebranding Rapibase: el dashboard usa las escalas genéricas de Tailwind
 * (gray/blue/green/...), así que en vez de tocar cada componente se remapea
 * la paleta completa aquí al sistema visual de rapibase.com — dark oliva
 * (ink) con acento lima. La inversión es consistente: los tonos claros
 * (50-300, fondos/bordes del tema claro) pasan a oscuros, y los oscuros
 * (600-900, textos/acentos) pasan a claros.
 */

// Neutros oliva — la base del tema.
const gray = {
  50: '#0c0f08', // fondo de página (ink)
  100: '#1a1f16', // superficies elevadas
  200: '#272e1f', // bordes
  300: '#323b28', // bordes marcados / hover
  400: '#6b7360', // texto atenuado
  500: '#97a188', // texto secundario (moss)
  600: '#a8b199',
  700: '#c3cab4',
  800: '#d8ddcb',
  900: '#e8eddf', // texto principal (bone)
}

// Primario: lima. bg-blue-600 + text-white queda lima con texto ink
// (white se mapea a superficie oscura, ver abajo).
const blue = {
  50: '#161b10',
  100: '#1d2413',
  200: '#39451f',
  300: '#57682b',
  400: '#8aa933',
  500: '#a8cf37',
  600: '#c4ef3d', // lima
  700: '#d6fa62', // lima brillante (hover)
  800: '#dff995',
  900: '#ecfcc0',
}

// Acentos, atenuados para fondo oscuro. Mantienen su semántica
// (verde=ok, rojo=peligro, ámbar=aviso…) sin gritar sobre el oliva.
const green = {
  50: '#121b10',
  100: '#172413',
  200: '#28401e',
  300: '#3c5c2b',
  400: '#6fbb55',
  500: '#84d364',
  600: '#9be457',
  700: '#b1f07a',
  800: '#d3f7b0',
  900: '#e6fbd4',
}

const red = {
  50: '#1f1211',
  100: '#2a1615',
  200: '#4a2321',
  300: '#6e312d',
  400: '#e07a72',
  500: '#e5675f',
  600: '#ef5a50',
  700: '#f4776e',
  800: '#f6b3ad',
  900: '#fbd7d3',
}

const amber = {
  50: '#1e1910',
  100: '#282112',
  200: '#48391c',
  300: '#6b5426',
  400: '#d9ae4e',
  500: '#e0b855',
  600: '#e6c15e',
  700: '#eccf7f',
  800: '#f4e2ae',
  900: '#f9f0d4',
}

const orange = {
  50: '#1f1710',
  100: '#2a1e12',
  200: '#4a341c',
  300: '#6e4a26',
  400: '#dc9a52',
  500: '#e2a55c',
  600: '#e8b066',
  700: '#eec287',
  800: '#f5dcb4',
  900: '#faeed8',
}

const purple = {
  50: '#161422',
  100: '#1c192b',
  200: '#332c4a',
  300: '#4c416e',
  400: '#a794dd',
  500: '#b3a2e2',
  600: '#bfb0e8',
  700: '#cec2ee',
  800: '#e2daf6',
  900: '#f0ecfa',
}

const teal = {
  50: '#101b19',
  100: '#142420',
  200: '#1f3f37',
  300: '#2c5c50',
  400: '#5ec9b4',
  500: '#6ad3c0',
  600: '#7dddcb',
  700: '#9ce7d9',
  800: '#c6f1e8',
  900: '#e2f8f3',
}

export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        // Las tarjetas usan bg-white y los botones primarios text-white:
        // mapeado a superficie oscura, ambos quedan bien (tarjeta oscura,
        // texto ink sobre lima).
        white: '#131711',
        gray,
        blue,
        green,
        red,
        amber,
        yellow: amber,
        orange,
        purple,
        indigo: purple,
        rose: red,
        teal,
      },
      fontFamily: {
        sans: ['system-ui', '"Segoe UI"', 'Roboto', 'sans-serif'],
        mono: ['"JetBrains Mono"', 'ui-monospace', 'Consolas', 'monospace'],
        display: ['"Bricolage Grotesque"', 'system-ui', 'sans-serif'],
      },
    },
  },
  plugins: [typography],
}
