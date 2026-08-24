/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        base: '#0B0B12',
        surface: '#14141F',
        raised: '#1C1C2B',
        hairline: 'rgba(255,255,255,0.08)',
        ink: {
          DEFAULT: '#F4F4F8',
          muted: '#8A8AA3',
          faint: '#5B5B72',
        },
        brand: {
          from: '#8B5CF6',
          to: '#D946EF',
          DEFAULT: '#A855F7',
        },
        danger: {
          from: '#F59E0B',
          to: '#EF4444',
          DEFAULT: '#F87171',
        },
        success: '#10B981',
        info: '#38BDF8',
      },
      fontFamily: {
        // 不引 web font：中文字体文件动辄几百 KB，首屏代价过高。
        sans: [
          '-apple-system',
          'BlinkMacSystemFont',
          '"PingFang SC"',
          '"Noto Sans SC"',
          '"Microsoft YaHei"',
          'system-ui',
          'sans-serif',
        ],
        // 倒计时用等宽：比例字体下数字宽度每秒抖动，观感很差。
        mono: ['ui-monospace', 'SFMono-Regular', 'Menlo', 'monospace'],
      },
      boxShadow: {
        card: '0 1px 2px rgba(0,0,0,.4), 0 8px 24px -12px rgba(0,0,0,.6)',
        glow: '0 0 0 1px rgba(168,85,247,.4), 0 8px 32px -8px rgba(168,85,247,.45)',
        'glow-danger': '0 0 0 1px rgba(248,113,113,.45), 0 8px 32px -8px rgba(239,68,68,.45)',
      },
      backgroundImage: {
        'brand-grad': 'linear-gradient(135deg, #8B5CF6 0%, #D946EF 100%)',
        'danger-grad': 'linear-gradient(135deg, #F59E0B 0%, #EF4444 100%)',
      },
      keyframes: {
        'slide-down': {
          from: { transform: 'translateY(-100%)', opacity: '0' },
          to: { transform: 'translateY(0)', opacity: '1' },
        },
        'pop-in': {
          from: { transform: 'scale(.96)', opacity: '0' },
          to: { transform: 'scale(1)', opacity: '1' },
        },
        'bubble-in': {
          from: { transform: 'translateY(6px)', opacity: '0' },
          to: { transform: 'translateY(0)', opacity: '1' },
        },
        shimmer: {
          '100%': { transform: 'translateX(100%)' },
        },
      },
      animation: {
        'slide-down': 'slide-down .22s ease-out',
        'pop-in': 'pop-in .18s ease-out',
        'bubble-in': 'bubble-in .16s ease-out',
      },
    },
  },
  plugins: [],
}
