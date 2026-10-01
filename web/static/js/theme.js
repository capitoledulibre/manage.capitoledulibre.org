// Applies the "system" theme before the page is painted and follows changes
// of the operating system preference. Explicit light/dark choices are
// rendered by the server.
(() => {
  const root = document.documentElement;
  if (root.dataset.theme !== "system") return;
  const query = window.matchMedia("(prefers-color-scheme: dark)");
  const apply = () => root.classList.toggle("dark", query.matches);
  apply();
  query.addEventListener("change", apply);
})();
