/**
 * 预加载脚本：把后端地址安全地暴露给渲染进程。
 * 页面由后端同源提供（http://127.0.0.1:<port>/），因此接口基址为空即可。
 */
const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('__FAM_API_BASE__', '');

// 调试用：查询后端实际端口
contextBridge.exposeInMainWorld('famBridge', {
  getPort: () => ipcRenderer.invoke('fam:port'),
});
