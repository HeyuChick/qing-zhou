(function () {
  const { app, api, store } = window.qz;

  window.addEventListener('hashchange', () => { store.route = location.hash.slice(1) || '/'; });

  async function boot() {
    try { store.cfg = await api('/api/config') || store.cfg; } catch (e) {}
    if (store.token) {
      try { store.user = await api('/api/auth/me'); await window.qz.loadNotices(); }
      catch (e) { store.token = ''; localStorage.removeItem('qz_token'); }
    }
    store.route = location.hash.slice(1) || '/';
    store.ready = true;
  }

  boot().then(() => app.mount('#app'));
})();
