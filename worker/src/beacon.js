/**
 * oshiete beacon.
 *
 * Drop this on any page and it reports one pageview. It stores nothing on the
 * device: no cookie, no localStorage, no sessionStorage, no fingerprint. The
 * only state it keeps lives in this closure and dies with the page.
 *
 *   <script defer src="https://o.5seg.top/b.js" data-site="blog"></script>
 *
 * `data-site` wins; without it the hostname is used, with `www.` removed and
 * dots turned into dashes.
 */
(function () {
  var ME = document.currentScript;
  var ENDPOINT = "https://o.5seg.top/e";

  // Opt-outs. Both are checked before anything is sent.
  if (navigator.doNotTrack === "1" || window.doNotTrack === "1" || navigator.globalPrivacyControl) return;
  // Headless drivers run JS happily; skip them.
  if (navigator.webdriver) return;

  var site =
    (ME && ME.getAttribute("data-site")) ||
    location.hostname.replace(/^www\./, "").replace(/\./g, "-");

  function ref() {
    if (!document.referrer) return null;
    try {
      var h = new URL(document.referrer).hostname.replace(/^www\./, "");
      // Same-site navigation is not an acquisition source.
      return h === location.hostname.replace(/^www\./, "") ? null : h;
    } catch (e) {
      return null;
    }
  }

  function send() {
    var body = JSON.stringify({
      v: 1,
      s: site,
      p: location.pathname,
      r: ref(),
      w: window.innerWidth,
      l: navigator.language,
    });

    // text/plain keeps this a CORS simple request, so there is no preflight.
    if (navigator.sendBeacon) {
      navigator.sendBeacon(ENDPOINT, new Blob([body], { type: "text/plain;charset=UTF-8" }));
    } else {
      fetch(ENDPOINT, { method: "POST", body: body, keepalive: true, mode: "no-cors" }).catch(function () {});
    }
  }

  send();

  // Static sites that use view transitions or an SPA router swap the DOM without
  // reloading, so the load event fires once for what is really several pageviews.
  var last = location.pathname;
  function route() {
    if (location.pathname === last) return;
    last = location.pathname;
    send();
  }
  addEventListener("popstate", route);
  var push = history.pushState;
  if (push) {
    history.pushState = function () {
      push.apply(this, arguments);
      route();
    };
    var replace = history.replaceState;
    history.replaceState = function () {
      replace.apply(this, arguments);
      route();
    };
  }
})();
