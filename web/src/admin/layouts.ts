// Companies' configs and their plant layouts, loaded once per page visit.
import { useEffect, useState } from 'react';
import { companyConfig, layoutCompany } from '@/config/loader';
import type { CompanyCfg, IndustryCfg } from '@/config/schema';
import type { PlantLayout } from '@/core/layout';
import * as api from './api';

export interface Loaded { company: CompanyCfg; industries: Map<string, IndustryCfg>; plants: PlantLayout[] }

const cache = new Map<string, Promise<Loaded>>();

export function loadCompany(id: string): Promise<Loaded> {
  let p = cache.get(id);
  if (!p) {
    p = companyConfig(id, api.industry).then(({ company, industries }) => ({ company, industries, plants: layoutCompany(company, industries) }));
    p.catch(() => cache.delete(id));
    cache.set(id, p);
  }
  return p;
}

/** Forget a company (it was replaced or deleted). */
export const forget = (id: string) => cache.delete(id);

export function useCompany(id: string | null): { data?: Loaded; error?: string } {
  const [state, setState] = useState<{ id: string | null; data?: Loaded; error?: string }>({ id: null });
  useEffect(() => {
    if (!id) return;
    let live = true;
    loadCompany(id).then((data) => live && setState({ id, data }), (e: Error) => live && setState({ id, error: e.message }));
    return () => { live = false; };
  }, [id]);
  return state.id === id ? state : {};
}

/** Every listed company's config, for templates; the ones that fail to load are left out. */
export function useCompanies(ids: string[]): Map<string, Loaded> {
  const [out, setOut] = useState(new Map<string, Loaded>());
  const key = ids.join(',');
  useEffect(() => {
    let live = true;
    Promise.all(ids.map((id) => loadCompany(id).then((d) => [id, d] as const, () => null))).then((all) => {
      if (live) setOut(new Map(all.filter((x) => x !== null)));
    });
    return () => { live = false; };
  }, [key]);
  return out;
}
