import {defineConfig} from '@playwright/test';
export default defineConfig({testDir:'./tests',use:{channel:'chromium',baseURL:process.env.WORKSPACE_URL||'http://127.0.0.1:5173',viewport:{width:1586,height:992}},reporter:[['list']],webServer:process.env.WORKSPACE_URL?undefined:{command:'npm run dev',url:'http://127.0.0.1:5173',reuseExistingServer:true}});
