import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import './index.css';
import { ConfigError, loadCompany } from '@/config/loader';
import { initRuntime, useUI } from '@/state/store';

const root = createRoot(document.getElementById('root')!);

function ConfigProblem({ error }: { error: unknown }) {
  const file = error instanceof ConfigError ? error.file : '加载配置时出错';
  const lines = error instanceof ConfigError ? error.issues : [String((error as Error)?.message ?? error)];
  return (
    <div className="grid h-full place-items-center bg-stage p-6">
      <div className="w-full max-w-xl rounded-2xl bg-card p-6 shadow-float">
        <h1 className="text-lg font-extrabold">配置有误，工厂无法加载</h1>
        <p className="mt-1 text-sm text-muted-foreground">改好下面这个文件后保存，页面会自动刷新。</p>
        <code className="mt-4 block rounded-lg bg-surface px-3 py-2 text-[13px] font-bold">{file}</code>
        <ul className="mt-3 grid gap-1.5 text-[13px] text-destructive">
          {lines.map((l, i) => <li key={i}>{l}</li>)}
        </ul>
      </div>
    </div>
  );
}

const params = new URLSearchParams(location.search);
loadCompany(params.get('company'))
  .then(async (bundle) => {
    const rt = initRuntime(bundle);
    const plant = rt.byId[params.get('plant') ?? ''] ?? rt.plants[0];
    useUI.getState().setPlant(plant.layout.id);
    const { default: App } = await import('./App');
    root.render(<StrictMode><App /></StrictMode>);
  })
  .catch((e) => {
    console.error(e);
    root.render(<ConfigProblem error={e} />);
  });
