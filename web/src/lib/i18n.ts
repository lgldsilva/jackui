import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import translationPT from '../locales/pt.json';
import translationEN from '../locales/en.json';

const savedLang = localStorage.getItem('jackui_language');
const defaultLang = savedLang || (navigator.language.startsWith('en') ? 'en-US' : 'pt-BR');

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
