// The builder a shape program writes against. It records a node tree in the
// same form as agent-go's scene mode (type, name, geometry, position, rotation,
// children, line); the host flattens it into SmartFactory parts. Nothing is
// drawn here.
var __shape = (function () {
  var LIMIT = (typeof __shapeMaxNodes === "number") ? __shapeMaxNodes : 400;
  var PALETTE = ["white", "shell", "blue", "blueDeep", "blueSoft", "yellow", "orange", "kraft", "wood", "grey", "greyDark", "ink", "metal", "glass", "red", "green", "mint"];
  var roots = [];
  var stack = [];
  var count = 0;
  var lineRe = /program\.js:(\d+)/;

  function callerLine() {
    var m = lineRe.exec(String(new Error().stack));
    return m ? Number(m[1]) : 0;
  }
  function fail(what, msg) { throw new Error(what + ": " + msg); }
  function num(what, field, v, positive) {
    if (typeof v !== "number" || !isFinite(v)) fail(what, field + " must be a finite number, got " + JSON.stringify(v));
    if (positive && !(v > 0)) fail(what, field + " must be greater than 0, got " + v);
    return v;
  }
  function vec(what, field, v) {
    if (v === undefined || v === null) return [0, 0, 0];
    if (Array.isArray(v)) {
      if (v.length !== 3) fail(what, field + " must have 3 components, got " + v.length);
      return [num(what, field + "[0]", v[0]), num(what, field + "[1]", v[1]), num(what, field + "[2]", v[2])];
    }
    if (typeof v === "object") {
      var keys = Object.keys(v);
      for (var i = 0; i < keys.length; i++) {
        if (keys[i] !== "x" && keys[i] !== "y" && keys[i] !== "z") fail(what, field + " has unknown key '" + keys[i] + "' (use x, y, z)");
      }
      return [
        v.x === undefined ? 0 : num(what, field + ".x", v.x),
        v.y === undefined ? 0 : num(what, field + ".y", v.y),
        v.z === undefined ? 0 : num(what, field + ".z", v.z)
      ];
    }
    fail(what, field + " must be {x,y,z} or [x,y,z], got " + JSON.stringify(v));
  }
  function color(what, field, c) {
    if (typeof c === "number" && isFinite(c) && c >= 0 && c <= 0xffffff) {
      var s = Math.floor(c).toString(16);
      while (s.length < 6) s = "0" + s;
      return "#" + s;
    }
    if (typeof c === "string" && /^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/.test(c)) return c.toLowerCase();
    if (typeof c === "string" && PALETTE.indexOf(c) >= 0) return c;
    fail(what, field + " must be '#rrggbb', 0xRRGGBB or one of " + PALETTE.join(", ") + "; got " + JSON.stringify(c));
  }
  function unit(what, field, v) {
    num(what, field, v);
    if (v < 0 || v > 1) fail(what, field + " must be between 0 and 1, got " + v);
    return v;
  }
  function checkKeys(what, obj, allowed) {
    var keys = Object.keys(obj);
    for (var i = 0; i < keys.length; i++) {
      if (allowed.indexOf(keys[i]) < 0) fail(what, "unknown option '" + keys[i] + "' (allowed: " + allowed.join(", ") + ")");
    }
  }
  function anim(what, a) {
    if (typeof a !== "object" || a === null) fail(what, "anim must be an object like {type: 'spin', axis: 'y', speed: 6}");
    checkKeys(what, a, ["type", "axis", "speed", "to", "window", "when"]);
    if (["spin", "stroke", "blink", "heat"].indexOf(a.type) < 0) fail(what, "anim.type must be spin, stroke, blink or heat");
    var out = { type: a.type };
    if (a.axis !== undefined) {
      if (["x", "y", "z"].indexOf(a.axis) < 0) fail(what, "anim.axis must be 'x', 'y' or 'z'");
      out.axis = a.axis;
    }
    if (a.speed !== undefined) out.speed = num(what, "anim.speed", a.speed);
    if (a.to !== undefined) out.to = num(what, "anim.to", a.to);
    if (a.window !== undefined) {
      if (!Array.isArray(a.window) || a.window.length !== 2) fail(what, "anim.window must be [from, to] between 0 and 1");
      out.window = [unit(what, "anim.window[0]", a.window[0]), unit(what, "anim.window[1]", a.window[1])];
    }
    if (a.when !== undefined) {
      if (a.when !== "running" && a.when !== "always") fail(what, "anim.when must be 'running' or 'always'");
      out.when = a.when;
    }
    return out;
  }
  function add(node) {
    count++;
    if (count > LIMIT) throw new Error("node limit reached: a shape may have at most " + LIMIT + " nodes");
    node.line = callerLine();
    if (stack.length) stack[stack.length - 1].children.push(node);
    else roots.push(node);
    return { name: node.name, type: node.type };
  }
  function mesh(type, what, geometry, pos, opts) {
    if (opts === undefined || opts === null) opts = {};
    if (typeof opts !== "object") fail(what, "options must be an object");
    checkKeys(what, opts, ["name", "color", "rotation", "metal", "rough", "glow", "opacity", "round", "anim"]);
    var node = { type: type, name: opts.name === undefined ? "" : String(opts.name), geometry: geometry, position: vec(what, "position", pos) };
    var rot = vec(what, "rotation", opts.rotation);
    if (rot[0] || rot[1] || rot[2]) node.rotation = rot;
    var m = { color: opts.color === undefined ? "white" : color(what, "color", opts.color) };
    if (opts.metal !== undefined) m.metal = unit(what, "metal", opts.metal);
    if (opts.rough !== undefined) m.rough = unit(what, "rough", opts.rough);
    if (opts.glow !== undefined) m.glow = color(what, "glow", opts.glow);
    if (opts.opacity !== undefined) m.opacity = unit(what, "opacity", opts.opacity);
    if (opts.round !== undefined) {
      if (type !== "box") fail(what, "round is for boxes only");
      m.round = num(what, "round", opts.round);
      if (m.round < 0) fail(what, "round must be >= 0");
    }
    if (opts.anim !== undefined) m.anim = anim(what, opts.anim);
    node.material = m;
    return add(node);
  }
  function geom(what, size, allowed) {
    if (typeof size !== "object" || size === null || Array.isArray(size)) fail(what, "size must be an object like {" + allowed.join(", ") + "}");
    checkKeys(what, size, allowed);
    return size;
  }

  var api = {
    box: function (size, position, opts) {
      var s = geom("shape.box", size, ["w", "h", "d"]);
      return mesh("box", "shape.box", { w: num("shape.box", "w", s.w, true), h: num("shape.box", "h", s.h, true), d: num("shape.box", "d", s.d, true) }, position, opts);
    },
    cylinder: function (size, position, opts) {
      var s = geom("shape.cylinder", size, ["r", "h"]);
      return mesh("cylinder", "shape.cylinder", { r: num("shape.cylinder", "r", s.r, true), h: num("shape.cylinder", "h", s.h, true) }, position, opts);
    },
    cone: function (size, position, opts) {
      var s = geom("shape.cone", size, ["r", "h"]);
      return mesh("cone", "shape.cone", { r: num("shape.cone", "r", s.r, true), h: num("shape.cone", "h", s.h, true) }, position, opts);
    },
    sphere: function (size, position, opts) {
      var s = geom("shape.sphere", size, ["r"]);
      return mesh("sphere", "shape.sphere", { r: num("shape.sphere", "r", s.r, true) }, position, opts);
    },
    torus: function (size, position, opts) {
      var s = geom("shape.torus", size, ["r", "tube"]);
      return mesh("torus", "shape.torus", { r: num("shape.torus", "r", s.r, true), tube: num("shape.torus", "tube", s.tube, true) }, position, opts);
    },
    capsule: function (size, position, opts) {
      var s = geom("shape.capsule", size, ["r", "len"]);
      return mesh("capsule", "shape.capsule", { r: num("shape.capsule", "r", s.r, true), len: num("shape.capsule", "len", s.len, true) }, position, opts);
    },
    group: function (name, transform, fn) {
      if (typeof transform === "function" && fn === undefined) { fn = transform; transform = {}; }
      if (typeof fn !== "function") fail("shape.group", "the last argument must be a function that adds the group's children");
      if (transform === undefined || transform === null) transform = {};
      if (typeof transform !== "object") fail("shape.group", "transform must be an object like {position, rotation}");
      checkKeys("shape.group", transform, ["position", "rotation"]);
      var node = { type: "group", name: String(name === undefined ? "" : name), position: vec("shape.group", "position", transform.position), children: [] };
      var rot = vec("shape.group", "rotation", transform.rotation);
      if (rot[0] || rot[1] || rot[2]) node.rotation = rot;
      var handle = add(node);
      stack.push(node);
      try { fn(); } finally { stack.pop(); }
      return handle;
    },
    count: function () { return count; }
  };

  return {
    api: api,
    export: function () { return { version: 1, units: "m", up: "y", nodes: roots }; }
  };
})();
var shape = new Proxy(__shape.api, {
  get: function (target, prop) {
    if (typeof prop === "string" && !(prop in target) &&
        ["toJSON", "then", "constructor", "valueOf", "toString", "inspect"].indexOf(prop) < 0) {
      throw new Error("shape." + prop + " does not exist; the shape API has " + Object.keys(target).join(", "));
    }
    return target[prop];
  }
});
var frame = (typeof __frame === "object" && __frame !== null) ? __frame : {};
