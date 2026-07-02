import tseslint from 'typescript-eslint'
import sonarjs from 'eslint-plugin-sonarjs'
import reactHooks from 'eslint-plugin-react-hooks'

// Baseline (auditoria 2026-07): arquivos que já violavam cognitive-complexity>15
// quando o gate nasceu. Neles a regra fica em 'warn' até serem fatiados
// (issues #416/#417) — NÃO adicione arquivos novos a esta lista; refatore.
const legacyComplexity = [
  'src/components/AddTorrentModal.tsx',
  'src/components/MoveFolderModal.tsx',
  'src/components/PlayerProvider.tsx',
  'src/components/ResultCard.tsx',
  'src/components/StreamCacheCard.tsx',
  'src/components/player/VideoPlayerElement.tsx',
  'src/lib/seriesGroup.ts',
  'src/lib/useFilteredResults.ts',
  'src/pages/SearchPage.tsx',
]

export default tseslint.config(
  { ignores: ['dist/**', 'node_modules/**'] },
  // Há eslint-disable herdados de tooling antigo (react-hooks etc.) cujos plugins
  // não estão carregados aqui — sem isto viram falso "Unused directive".
  { linterOptions: { reportUnusedDisableDirectives: 'off' } },
  {
    files: ['src/**/*.{ts,tsx}'],
    languageOptions: { parser: tseslint.parser },
    plugins: { sonarjs, 'react-hooks': reactHooks },
    rules: {
      // Espelha o S3776 do SonarQube (limite 15) que o gate da main aplica —
      // aqui ele quebra JÁ no PR, antes de queimar um ciclo de CI de ~12min.
      'sonarjs/cognitive-complexity': ['error', 15],
      // Registrada mas desligada: o código tem eslint-disable herdados desta
      // regra; sem a definição o ESLint erra "rule not found". Ligar (warn) é
      // um follow-up — hoje o gate é só complexidade.
      'react-hooks/exhaustive-deps': 'off',
    },
  },
  {
    files: legacyComplexity,
    rules: { 'sonarjs/cognitive-complexity': ['warn', 15] },
  },
)
