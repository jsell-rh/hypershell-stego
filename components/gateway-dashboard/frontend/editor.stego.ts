import * as monaco from 'monaco-editor/esm/vs/editor/editor.api';
import { loader } from '@monaco-editor/react';

// Use the captured local editor before React mounts the dashboard.
loader.config({ monaco });
