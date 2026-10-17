// The waiter's phone page (waiter.html, served for /waiter): tap a table to report it.
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import '../index.css';
import Waiter from './Waiter';

createRoot(document.getElementById('root')!).render(<StrictMode><Waiter /></StrictMode>);
