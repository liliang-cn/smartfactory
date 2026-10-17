// Asset-type pictograms in the SmartFactory palette (static, trusted SVG strings).
import { cn } from '@/lib/utils';

export const ICON: Record<string, string> = {
  cnc: '<svg viewBox="0 0 26 26"><rect x="3" y="5" width="20" height="16" rx="3" fill="#2f5bea"/><rect x="6" y="8" width="9" height="8" rx="1.5" fill="#bcd0ff"/><rect x="17" y="8" width="3" height="3" rx="1" fill="#fff"/><rect x="3" y="19" width="20" height="3" rx="1.2" fill="#1f3fb0"/></svg>',
  press: '<svg viewBox="0 0 26 26"><rect x="4" y="3" width="18" height="5" rx="2" fill="#2f5bea"/><rect x="5" y="8" width="3" height="12" fill="#9db4ff"/><rect x="18" y="8" width="3" height="12" fill="#9db4ff"/><rect x="9" y="10" width="8" height="4" rx="1" fill="#1f3fb0"/><rect x="3" y="20" width="20" height="3" rx="1.2" fill="#1f3fb0"/></svg>',
  robot: '<svg viewBox="0 0 26 26"><rect x="6" y="19" width="10" height="4" rx="1.5" fill="#1f3fb0"/><path d="M11 19V13l6-6 3 3" stroke="#f6c445" stroke-width="3.4" fill="none" stroke-linecap="round" stroke-linejoin="round"/><circle cx="11" cy="13" r="2.2" fill="#2f5bea"/><circle cx="17" cy="7" r="2" fill="#2f5bea"/></svg>',
  oven: '<svg viewBox="0 0 26 26"><rect x="3" y="7" width="20" height="13" rx="3" fill="#2f5bea"/><rect x="6" y="11" width="14" height="5" rx="1.5" fill="#ff8a3d"/><rect x="7" y="3" width="3" height="4" fill="#9db4ff"/><rect x="16" y="3" width="3" height="4" fill="#9db4ff"/></svg>',
  qc: '<svg viewBox="0 0 26 26"><path d="M5 22V6h16v16" stroke="#2f5bea" stroke-width="2.6" fill="none" stroke-linecap="round"/><rect x="10" y="8" width="6" height="5" rx="1.5" fill="#1f3fb0"/><path d="M9 20l4-6 4 6z" fill="#bcd0ff"/></svg>',
  pack: '<svg viewBox="0 0 26 26"><path d="M4 9l9-5 9 5v10l-9 5-9-5z" fill="#d9a46b"/><path d="M4 9l9 5 9-5M13 14v10" stroke="#bf8a52" stroke-width="1.6" fill="none"/></svg>',
  conveyor: '<svg viewBox="0 0 26 26"><rect x="3" y="13" width="20" height="5" rx="2.5" fill="#2f5bea"/><circle cx="7" cy="15.5" r="1.4" fill="#fff"/><circle cx="13" cy="15.5" r="1.4" fill="#fff"/><circle cx="19" cy="15.5" r="1.4" fill="#fff"/><rect x="6" y="7" width="5" height="5" rx="1" fill="#d9a46b"/><rect x="14" y="7" width="5" height="5" rx="1" fill="#d9a46b"/></svg>',
  agv: '<svg viewBox="0 0 26 26"><rect x="3" y="12" width="20" height="7" rx="3" fill="#f6c445"/><rect x="5" y="7" width="12" height="5" rx="1" fill="#d9a46b"/><circle cx="8" cy="20" r="2" fill="#28314b"/><circle cx="18" cy="20" r="2" fill="#28314b"/><rect x="19" y="10" width="3" height="2" rx="1" fill="#28314b"/></svg>',
  truck: '<svg viewBox="0 0 26 26"><rect x="2" y="6" width="14" height="12" rx="2" fill="#fff" stroke="#2f5bea" stroke-width="1.6"/><path d="M16 10h4l3 4v4h-7z" fill="#2f5bea"/><circle cx="7" cy="19" r="2.2" fill="#28314b"/><circle cx="19" cy="19" r="2.2" fill="#28314b"/></svg>',
  buffer: '<svg viewBox="0 0 26 26"><rect x="4" y="19" width="18" height="3" rx="1" fill="#c79a68"/><rect x="5" y="14" width="7" height="5" rx="1" fill="#aab6d3"/><rect x="13" y="14" width="7" height="5" rx="1" fill="#aab6d3"/><rect x="9" y="9" width="7" height="5" rx="1" fill="#aab6d3"/></svg>',
  outfeed: '<svg viewBox="0 0 26 26"><rect x="4" y="19" width="18" height="3" rx="1" fill="#c79a68"/><rect x="5" y="13" width="7" height="6" rx="1" fill="#d9a46b"/><rect x="13" y="13" width="7" height="6" rx="1" fill="#d9a46b"/><rect x="9" y="7" width="7" height="6" rx="1" fill="#d9a46b"/></svg>',
  warehouse: '<svg viewBox="0 0 26 26"><path d="M3 10l10-6 10 6v12H3z" fill="#2f5bea"/><rect x="7" y="13" width="12" height="9" fill="#e8eefe"/><rect x="9" y="15" width="3" height="3" fill="#d9a46b"/><rect x="14" y="15" width="3" height="3" fill="#d9a46b"/></svg>',
  fgzone: '<svg viewBox="0 0 26 26"><rect x="3" y="4" width="20" height="18" rx="3" fill="#fff3d0" stroke="#f6c445" stroke-width="1.6" stroke-dasharray="3 2"/><rect x="7" y="9" width="5" height="5" rx="1" fill="#d9a46b"/><rect x="14" y="9" width="5" height="5" rx="1" fill="#d9a46b"/><rect x="10" y="15" width="5" height="5" rx="1" fill="#d9a46b"/></svg>',
};
ICON.assembly = '<svg viewBox="0 0 26 26"><circle cx="9" cy="7" r="3" fill="#f1c7a5"/><path d="M4 21v-5a5 5 0 0 1 10 0v5z" fill="#5b8cff"/><circle cx="18" cy="8" r="3" fill="#f1c7a5"/><path d="M13 21v-5a5 5 0 0 1 10 0v5z" fill="#2f5bea"/></svg>';
ICON.safety = '<svg viewBox="0 0 26 26"><path d="M13 3 3 21h20z" fill="#f6c445"/><path d="M14 8l-3 6h3l-2 5 5-7h-3l2-4z" fill="#28314b"/></svg>';
ICON.leak = '<svg viewBox="0 0 26 26"><rect x="3" y="6" width="20" height="15" rx="3" fill="#2f5bea"/><rect x="6" y="9" width="14" height="9" rx="2" fill="#d9ccff"/><circle cx="13" cy="13.5" r="2.4" fill="#7c4ddb"/></svg>';
ICON.charge = '<svg viewBox="0 0 26 26"><rect x="4" y="5" width="11" height="17" rx="2.5" fill="#2f5bea"/><rect x="6.5" y="8" width="6" height="4" rx="1" fill="#bcd0ff"/><rect x="17" y="10" width="5" height="12" rx="2.5" fill="#8fb4ff"/></svg>';
ICON.runtest = '<svg viewBox="0 0 26 26"><rect x="3" y="5" width="20" height="16" rx="3" fill="#2f5bea"/><rect x="6" y="9" width="14" height="9" rx="1.5" fill="#fff"/><circle cx="11" cy="13.5" r="3" fill="none" stroke="#28314b" stroke-width="1.5"/><path d="M11 10.5v6M8 13.5h6" stroke="#28314b" stroke-width="1.2"/></svg>';
ICON.tank = '<svg viewBox="0 0 26 26"><rect x="7" y="5" width="12" height="15" rx="5" fill="#2f5bea"/><rect x="11.5" y="8" width="3" height="9" rx="1.5" fill="#f2d39b"/><rect x="11" y="2" width="4" height="3" rx="1" fill="#28314b"/><path d="M8 20l-1 3M18 20l1 3" stroke="#58627a" stroke-width="1.6"/></svg>';
ICON.roller = '<svg viewBox="0 0 26 26"><rect x="3" y="4" width="3" height="18" rx="1" fill="#2f5bea"/><rect x="20" y="4" width="3" height="18" rx="1" fill="#2f5bea"/><circle cx="13" cy="9" r="4" fill="#b7c1d8"/><circle cx="13" cy="18" r="3" fill="#b7c1d8"/><path d="M6 13.5h14" stroke="#28314b" stroke-width="1.4"/></svg>';
ICON.frame = ICON.cnc;
ICON.table = '<svg viewBox="0 0 26 26"><rect x="4" y="9" width="18" height="4" rx="1.5" fill="#c79a68"/><rect x="12" y="13" width="2.4" height="8" fill="#58627a"/><circle cx="6" cy="18" r="2.6" fill="#e2412b"/><circle cx="20" cy="18" r="2.6" fill="#e2412b"/><circle cx="13" cy="8" r="2.4" fill="#d9361e"/></svg>';

/** Archetype -> pictogram for machines; asset type for everything else. */
const BY_ARCH: Record<string, string> = {
  enclosure: 'cnc', press: 'press', robot: 'robot', tunnel: 'oven', gantry: 'qc', packer: 'pack', manual: 'assembly',
  safety: 'safety', chamber: 'leak', filler: 'charge', booth: 'runtest', tank: 'tank', roller: 'roller', frame: 'frame',
};
export const iconKey = (a: { type: string; arch: string }) => (a.type === 'station' ? BY_ARCH[a.arch] ?? 'cnc' : a.type);

export function AssetIcon({ asset, className }: { asset: { type: string; arch: string }; className?: string }) {
  return <span className={cn('inline-grid [&>svg]:size-full', className)} dangerouslySetInnerHTML={{ __html: ICON[iconKey(asset)] ?? '' }} />;
}
