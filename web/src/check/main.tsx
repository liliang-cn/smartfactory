// The phone page's own entry (check.html, served for /check): filling forms in at the machines.
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import '../index.css';
import Check from './Check';

createRoot(document.getElementById('root')!).render(<StrictMode><Check /></StrictMode>);
