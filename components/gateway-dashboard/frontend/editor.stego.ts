import * as monaco from 'monaco-editor';
import { loader } from '@monaco-editor/react';

// Use the captured local editor before React mounts the dashboard.
loader.config({ monaco });
