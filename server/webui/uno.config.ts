import { defineConfig } from 'unocss'
import { presetWind3 } from '@unocss/preset-wind3'

export default defineConfig({
  presets: [presetWind3()],
  shortcuts: {
    glass: 'border border-white/10 bg-white/7 backdrop-blur-xl shadow-xl shadow-black/15',
    panel: 'glass rounded-5 p-5',
    btn: 'inline-flex items-center justify-center gap-2 rounded-3 px-4 py-2 text-sm font-600 transition disabled:cursor-not-allowed disabled:opacity-45',
    'btn-primary': 'btn bg-cyan-400 text-slate-950 hover:bg-cyan-300',
    'btn-secondary': 'btn border border-white/12 bg-white/8 text-slate-100 hover:bg-white/13',
    'btn-danger': 'btn border border-red-400/30 bg-red-400/12 text-red-100 hover:bg-red-400/20',
    input: 'w-full rounded-3 border border-white/12 bg-slate-950/55 px-3 py-2.5 text-slate-100 outline-none placeholder:text-slate-500 focus:border-cyan-300/70 focus:ring-2 focus:ring-cyan-300/12',
    label: 'mb-1.5 block text-xs font-600 uppercase tracking-wider text-slate-400',
  },
})
