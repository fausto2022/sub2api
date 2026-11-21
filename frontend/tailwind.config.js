/**
 * sub2api · 设计令牌（方向 B「Indigo & Cyan」，默认实现）
 *
 * 令牌契约（前端所有成员复用，语义不可改）：
 * - primary-* : 品牌主色（靛青 Indigo）。实心填充用 600/700（白字 ≥4.5:1），
 *               正文/链接用 600（亮色）/ 400（暗色）。
 * - dark-*    : 暗色表面 ink 色阶，950 = 最深背景，50 = 最浅。只用于 dark: 变体。
 * - accent-*  : 中性灰阶（slate），语义保持"灰"不变（大量组件当灰阶使用）。
 * - ink       : dark-* 的语义别名（同值），新代码可写 bg-ink-900。
 * 详见 design-system/sub2api/MASTER.md（含实测对比度表）。
 *
 * @type {import('tailwindcss').Config}
 */

// 暗色表面 ink 色阶（950 深 → 50 浅）
const ink = {
  950: '#070B14',
  900: '#0B1120',
  800: '#131C31',
  700: '#1E2740',
  600: '#2C3852',
  500: '#475573',
  400: '#7A88A6', // 较 design.md 原值 #6E7C9A 提亮一档：在 dark-800 卡面达 4.77:1（原 4.05 不达 AA）
  300: '#9AA6BE',
  200: '#C3CCDC',
  100: '#E2E7F0',
  50: '#F5F7FB'
}

// 品牌主色靛青 Indigo（70 steps = Tailwind indigo）
const indigoPrimary = {
  50: '#EEF2FF',
  100: '#E0E7FF',
  200: '#C7D2FE',
  300: '#A5B4FC',
  400: '#818CF8',
  500: '#6366F1',
  600: '#4F46E5',
  700: '#4338CA',
  800: '#3730A3',
  900: '#312E81',
  950: '#1E1B4B'
}

// 中性灰阶（slate）—— 与原 accent 完全一致，仅数值不变、语义不变
const slateAccent = {
  50: '#f8fafc',
  100: '#f1f5f9',
  200: '#e2e8f0',
  300: '#cbd5e1',
  400: '#94a3b8',
  500: '#64748b',
  600: '#475569',
  700: '#334155',
  800: '#1e293b',
  900: '#0f172a',
  950: '#020617'
}

export default {
  content: ['./index.html', './src/**/*.{vue,js,ts,jsx,tsx}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        primary: indigoPrimary,
        accent: slateAccent,
        dark: ink,
        ink,
        // 语义色（单值工具类：bg-success / text-danger …）
        // 实心填充取 -700 以满足白字 ≥4.5:1；text-danger 取 -600 已达标（4.83:1）
        success: '#047857',
        warning: '#B45309',
        danger: '#DC2626',
        info: '#0E7490'
      },
      fontFamily: {
        // 仅系统栈：不引入任何外部字体 CDN（中文 PingFang / 微软雅黑）
        sans: [
          'system-ui',
          '-apple-system',
          'BlinkMacSystemFont',
          'Segoe UI',
          'Roboto',
          'Helvetica Neue',
          'Arial',
          'PingFang SC',
          'Hiragino Sans GB',
          'Microsoft YaHei',
          'sans-serif'
        ],
        mono: ['ui-monospace', 'SFMono-Regular', 'Menlo', 'Monaco', 'Consolas', 'monospace']
      },
      boxShadow: {
        glass: '0 8px 32px rgba(7, 11, 20, 0.08)',
        'glass-sm': '0 4px 16px rgba(7, 11, 20, 0.06)',
        glow: '0 0 20px rgba(99, 102, 241, 0.28)',
        'glow-lg': '0 0 40px rgba(99, 102, 241, 0.38)',
        card: '0 1px 3px rgba(7, 11, 20, 0.04), 0 1px 2px rgba(7, 11, 20, 0.06)',
        'card-hover': '0 10px 40px rgba(7, 11, 20, 0.08)',
        'inner-glow': 'inset 0 1px 0 rgba(255, 255, 255, 0.1)'
      },
      backgroundImage: {
        'gradient-radial': 'radial-gradient(var(--tw-gradient-stops))',
        // CTA / 强调渐变：靛青 → 青（仅装饰；承载白字的实心填充请用 primary-600/700）
        'gradient-primary': 'linear-gradient(135deg, #6366F1 0%, #06B6D4 100%)',
        'gradient-dark': 'linear-gradient(135deg, #131C31 0%, #070B14 100%)',
        'gradient-glass':
          'linear-gradient(135deg, rgba(255,255,255,0.1) 0%, rgba(255,255,255,0.05) 100%)',
        'mesh-gradient':
          'radial-gradient(at 40% 20%, rgba(99, 102, 241, 0.14) 0px, transparent 50%), radial-gradient(at 80% 0%, rgba(6, 182, 212, 0.10) 0px, transparent 50%), radial-gradient(at 0% 50%, rgba(99, 102, 241, 0.10) 0px, transparent 50%)'
      },
      animation: {
        // 名称全部保留（组件已在用，禁止改名）
        'fade-in': 'fadeIn 0.3s ease-out',
        'slide-up': 'slideUp 0.3s ease-out',
        'slide-down': 'slideDown 0.3s ease-out',
        'slide-in-right': 'slideInRight 0.3s ease-out',
        'scale-in': 'scaleIn 0.2s ease-out',
        'pulse-slow': 'pulse 3s cubic-bezier(0.4, 0, 0.6, 1) infinite',
        shimmer: 'shimmer 2s linear infinite',
        glow: 'glow 2s ease-in-out infinite alternate'
      },
      keyframes: {
        fadeIn: {
          '0%': { opacity: '0' },
          '100%': { opacity: '1' }
        },
        slideUp: {
          '0%': { opacity: '0', transform: 'translateY(10px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' }
        },
        slideDown: {
          '0%': { opacity: '0', transform: 'translateY(-10px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' }
        },
        slideInRight: {
          '0%': { opacity: '0', transform: 'translateX(20px)' },
          '100%': { opacity: '1', transform: 'translateX(0)' }
        },
        scaleIn: {
          '0%': { opacity: '0', transform: 'scale(0.95)' },
          '100%': { opacity: '1', transform: 'scale(1)' }
        },
        shimmer: {
          '0%': { backgroundPosition: '-200% 0' },
          '100%': { backgroundPosition: '200% 0' }
        },
        glow: {
          '0%': { boxShadow: '0 0 20px rgba(99, 102, 241, 0.28)' },
          '100%': { boxShadow: '0 0 30px rgba(99, 102, 241, 0.42)' }
        }
      },
      backdropBlur: {
        xs: '2px'
      },
      borderRadius: {
        '4xl': '2rem'
      }
    }
  },
  plugins: []
}