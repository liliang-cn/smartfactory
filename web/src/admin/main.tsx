// The console's own entry (admin.html, served for /admin/*): a React app apart from the twin.
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import '../index.css';
import Console from './Console';

createRoot(document.getElementById('root')!).render(<StrictMode><Console /></StrictMode>);
