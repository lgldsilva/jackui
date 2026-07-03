import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import translationPT from '../locales/pt.json';
import translationEN from '../locales/en.json';

// Guarded so the module can be imported outside the browser (e.g. Vitest's node
// env, where pure-function tests transitively import components that import this).
const savedLang = typeof localStorage !== 'undefined' ? localStorage.getItem('jackui_language') : null;
const browserLang = typeof navigator !== 'undefined' ? navigator.language : '';
const defaultLang = savedLang || (browserLang.startsWith('en') ? 'en-US' : 'pt-BR');

i18n
  .use(initReactI18next)
  .init({
    resources: {
      'pt-BR': {
        translation: translationPT,
      },
      'en-US': {
        translation: translationEN,
      },
    },
    lng: defaultLang,
    fallbackLng: 'pt-BR',
    interpolation: {
      escapeValue: false,
    },
  });

export default i18n;
