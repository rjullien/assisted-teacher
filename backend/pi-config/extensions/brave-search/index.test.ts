// Tests de l'extension web_search.
//
// Aucun appel à l'API Brave réelle et aucune clé nécessaire : chaque test
// remplace globalThis.fetch. Exécution :
//   node --experimental-strip-types --test index.test.ts
//
// Chaque assertion a été vérifiée par mutation : en réintroduisant le défaut
// qu'elle couvre, le test échoue.

import assert from "node:assert/strict";
import { afterEach, describe, it } from "node:test";
import extension, { braveSearch, formatResults, readApiKey } from "./index.ts";

// Écrite en dur, PAS importée depuis index.ts. Construire l'attente depuis la
// constante rendait le test incapable d'échouer : vider FALLBACK laissait les
// assertions vertes. Cette phrase est un contrat partagé avec
// internal/bridge/brave.go, donc elle doit être dupliquée pour être vérifiée.
const FALLBACK_SENTENCE = "Cherche par toi-même avec tes propres capacités de recherche.";

const realFetch = globalThis.fetch;
afterEach(() => {
	globalThis.fetch = realFetch;
});

/** Remplace fetch et retourne la liste des requêtes vues. */
function stubFetch(handler: (url: string, init: RequestInit) => Response) {
	const seen: Array<{ url: string; init: RequestInit }> = [];
	globalThis.fetch = ((url: string | URL, init: RequestInit = {}) => {
		seen.push({ url: String(url), init });
		return Promise.resolve(handler(String(url), init));
	}) as typeof fetch;
	return seen;
}

function braveOk(results: unknown[]): Response {
	return new Response(JSON.stringify({ web: { results } }), {
		status: 200,
		headers: { "Content-Type": "application/json" },
	});
}

describe("readApiKey", () => {
	it("retire le \\n final des secrets Infisical", () => {
		assert.equal(readApiKey({ BRAVE_SEARCH_API_KEY: "abc123\n" }), "abc123");
	});

	it("traite une clé blanche comme absente", () => {
		// Une clé Infisical créée vide vaut "\n", qui est truthy. Sans le trim, on
		// partirait en appel réseau pour récolter un 422 de Brave au lieu du
		// message clair « clé non définie ».
		assert.equal(readApiKey({ BRAVE_SEARCH_API_KEY: "\n" }), "");
	});

	it("retourne une chaîne vide quand la variable est absente", () => {
		assert.equal(readApiKey({}), "");
	});
});

describe("braveSearch", () => {
	it("authentifie via le header X-Subscription-Token", async () => {
		const seen = stubFetch(() => braveOk([]));
		await braveSearch({ apiKey: "ma-cle", query: "x" });
		assert.equal(
			(seen[0].init.headers as Record<string, string>)["X-Subscription-Token"],
			"ma-cle",
		);
	});

	it("transmet query, count, country et freshness", async () => {
		const seen = stubFetch(() => braveOk([]));
		await braveSearch({ apiKey: "k", query: "programme cycle 3", count: 3, freshness: "pw" });
		const url = new URL(seen[0].url);
		assert.equal(url.searchParams.get("q"), "programme cycle 3");
		assert.equal(url.searchParams.get("count"), "3");
		assert.equal(url.searchParams.get("freshness"), "pw");
		assert.equal(url.searchParams.get("country"), "FR");
	});

	it("borne count à 20 et à 1", async () => {
		const seen = stubFetch(() => braveOk([]));
		await braveSearch({ apiKey: "k", query: "x", count: 999 });
		await braveSearch({ apiKey: "k", query: "x", count: 0 });
		assert.equal(new URL(seen[0].url).searchParams.get("count"), "20");
		assert.equal(new URL(seen[1].url).searchParams.get("count"), "1");
	});

	it("retire le balisage <strong> que Brave met sur les termes de la requête", async () => {
		stubFetch(() =>
			braveOk([
				{
					title: "Programme <strong>cycle 3</strong>",
					url: "https://eduscol.education.fr/c3",
					description: "Attendus de <strong>fin de cycle</strong>.",
				},
			]),
		);
		const [r] = await braveSearch({ apiKey: "k", query: "cycle 3" });
		assert.equal(r.title, "Programme cycle 3");
		assert.equal(r.description, "Attendus de fin de cycle.");
	});

	it("tolère une réponse sans bloc web", async () => {
		stubFetch(() => new Response("{}", { status: 200 }));
		assert.deepEqual(await braveSearch({ apiKey: "k", query: "x" }), []);
	});

	it("joint le status ET le corps au message d'erreur HTTP", async () => {
		// Sans le corps, un 4xx de Brave est indiagnosticable : quota dépassé,
		// clé révoquée et plan insuffisant renvoient tous des 4xx.
		stubFetch(() => new Response("quota exceeded", { status: 429, statusText: "Too Many Requests" }));
		await assert.rejects(braveSearch({ apiKey: "k", query: "x" }), (e: Error) => {
			assert.match(e.message, /429/);
			assert.match(e.message, /quota exceeded/);
			return true;
		});
	});

	it("propage le signal d'annulation à fetch", async () => {
		const controller = new AbortController();
		const seen = stubFetch(() => braveOk([]));
		await braveSearch({ apiKey: "k", query: "x", signal: controller.signal });
		assert.equal(seen[0].init.signal, controller.signal);
	});
});

describe("l'outil web_search", () => {
	/** Enregistre l'outil comme le ferait pi et le retourne. */
	function tool() {
		let registered: any = null;
		extension({ registerTool: (t: any) => (registered = t), on() {}, registerCommand() {} } as never);
		assert.ok(registered, "l'extension n'a rien enregistré");
		return registered;
	}

	const textOf = (r: any) => r.content[0].text as string;

	it("s'enregistre sous le nom attendu par piAllowedTools", () => {
		// Ce nom est repris tel quel dans --tools côté Go (piAllowedTools). Un
		// renommage ici sans mise à jour là-bas ferait disparaître l'outil en
		// silence : --tools n'est pas validé par pi.
		assert.equal(tool().name, "web_search");
	});

	it("exige query et accepte count et freshness", () => {
		const p = tool().parameters;
		assert.deepEqual(p.required, ["query"]);
		assert.ok(p.properties.count);
		assert.ok(p.properties.freshness);
	});

	it("rend les résultats quand la recherche aboutit", async () => {
		process.env.BRAVE_SEARCH_API_KEY = "k";
		stubFetch(() => braveOk([{ title: "T", url: "https://ex.test/a", description: "D" }]));
		const text = textOf(await tool().execute("id", { query: "x" }, undefined));
		assert.match(text, /https:\/\/ex\.test\/a/);
		assert.doesNotMatch(text, /indisponible/);
	});

	// La règle produit : un échec de recherche ne doit jamais terminer le tour sur
	// une erreur d'outil. Lever ici ferait exactement ça.
	it("dégrade au lieu de lever quand Brave répond une erreur", async () => {
		process.env.BRAVE_SEARCH_API_KEY = "k";
		stubFetch(() => new Response("quota", { status: 429, statusText: "Too Many Requests" }));
		const r = await tool().execute("id", { query: "x" }, undefined);
		assert.match(textOf(r), /Recherche Brave indisponible/);
		assert.ok(
			textOf(r).endsWith(FALLBACK_SENTENCE),
			`le message dégradé doit finir par la phrase de repli, obtenu : ${textOf(r)}`,
		);
	});

	it("dégrade au lieu de lever quand le réseau tombe", async () => {
		process.env.BRAVE_SEARCH_API_KEY = "k";
		globalThis.fetch = (() => Promise.reject(new Error("ECONNREFUSED"))) as typeof fetch;
		const r = await tool().execute("id", { query: "x" }, undefined);
		assert.match(textOf(r), /ECONNREFUSED/);
		assert.ok(
			textOf(r).endsWith(FALLBACK_SENTENCE),
			`le message dégradé doit finir par la phrase de repli, obtenu : ${textOf(r)}`,
		);
	});

	it("dégrade sans appel réseau quand la clé est absente", async () => {
		delete process.env.BRAVE_SEARCH_API_KEY;
		let called = false;
		globalThis.fetch = (() => {
			called = true;
			return Promise.resolve(braveOk([]));
		}) as typeof fetch;
		const r = await tool().execute("id", { query: "x" }, undefined);
		assert.equal(called, false, "un appel Brave est parti sans clé configurée");
		assert.match(textOf(r), /BRAVE_SEARCH_API_KEY non configurée/);
	});

	it("dégrade sans appel réseau sur une query vide", async () => {
		process.env.BRAVE_SEARCH_API_KEY = "k";
		let called = false;
		globalThis.fetch = (() => {
			called = true;
			return Promise.resolve(braveOk([]));
		}) as typeof fetch;
		const r = await tool().execute("id", { query: "   " }, undefined);
		assert.equal(called, false, "un appel Brave est parti avec une query vide");
		assert.match(textOf(r), /requête vide/);
	});
});

describe("formatResults", () => {
	it("expose le lien de chaque résultat pour que le modèle puisse citer sa source", () => {
		const text = formatResults("q", [
			{ title: "T", url: "https://example.test/a", description: "D", age: "2 days ago" },
		]);
		assert.match(text, /https:\/\/example\.test\/a/);
		assert.match(text, /2 days ago/);
	});

	it("annonce explicitement l'absence de résultat", () => {
		assert.match(formatResults("licorne", []), /Aucun résultat/);
	});
});
