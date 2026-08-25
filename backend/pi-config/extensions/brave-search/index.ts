// Recherche web pour pi, via l'API Brave Search.
//
// Pourquoi une extension et pas le skill brave-search de badlogic/pi-skills :
//
//  1. Le skill est un script Node que le modèle lance par le shell. Or `bash`
//     est volontairement absent de defaultTools (settings.json) — le modèle ne
//     doit pas avoir de shell dans ce pod. Les outils d'extension, eux, restent
//     actifs indépendamment de defaultTools, donc bash reste fermé.
//  2. Le skill tire @mozilla/readability, jsdom, turndown et turndown-plugin-gfm.
//     Ici, zéro dépendance : fetch est native en Node 22.
//  3. Le skill lit process.env.BRAVE_API_KEY sans trim. Voir readApiKey pour
//     pourquoi ça compte.
//
// La variable est BRAVE_SEARCH_API_KEY : le même nom que celui qu'impose
// nousresearch/hermes-agent, pour n'avoir qu'un seul nom dans tout le cluster.

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";

const ENDPOINT = "https://api.search.brave.com/res/v1/web/search";
const ENV_VAR = "BRAVE_SEARCH_API_KEY";
const DEFAULT_COUNT = 5;
const MAX_COUNT = 20;

// Même phrase de repli que internal/bridge/brave.go côté Lya/Desk. Un échec de
// recherche n'est jamais une erreur de job : le modèle est invité à continuer
// avec ses propres moyens plutôt que de terminer le tour sur « je ne peux pas
// chercher ». L'enseignante voit une réponse, pas un outil en erreur.
export const FALLBACK = "Cherche par toi-même avec tes propres capacités de recherche.";

/** Message dégradé, du même format que brave.go. */
export function degraded(reason: string): string {
	return `Recherche Brave indisponible (${reason}). ${FALLBACK}`;
}

export interface BraveResult {
	title: string;
	url: string;
	description: string;
	age?: string;
}

export function readApiKey(env: NodeJS.ProcessEnv = process.env): string {
	// Les secrets venant d'Infisical portent régulièrement un \n final.
	//
	// Ce trim n'est PAS là pour protéger le header : vérifié, undici retire de
	// lui-même les espaces en fin de valeur de header, donc `\n` ne casse pas la
	// requête et ne lève pas d'exception.
	//
	// Il est là pour que le test de vacuité ci-dessous soit juste. Une clé
	// Infisical créée vide vaut "\n", ce qui est truthy : sans trim on partirait
	// en appel réseau pour récolter un 422 VALIDATION de Brave, au lieu du
	// message clair « la clé n'est pas définie ». Couvert par le test
	// « clé blanche traitée comme absente ».
	return (env[ENV_VAR] ?? "").trim();
}

function stripHtml(value: string): string {
	// Brave met en gras les termes de la requête avec <strong>.
	return value.replace(/<[^>]*>/g, "");
}

export async function braveSearch(options: {
	apiKey: string;
	query: string;
	count?: number;
	country?: string;
	freshness?: string;
	signal?: AbortSignal;
}): Promise<BraveResult[]> {
	const params = new URLSearchParams({
		q: options.query,
		count: String(Math.min(Math.max(options.count ?? DEFAULT_COUNT, 1), MAX_COUNT)),
		country: options.country ?? "FR",
	});
	if (options.freshness) params.set("freshness", options.freshness);

	const response = await fetch(`${ENDPOINT}?${params.toString()}`, {
		headers: {
			Accept: "application/json",
			"Accept-Encoding": "gzip",
			"X-Subscription-Token": options.apiKey,
		},
		signal: options.signal,
	});

	if (!response.ok) {
		// Le corps porte le motif réel (quota dépassé, clé révoquée, plan
		// insuffisant). Sans lui, un 4xx est indiagnosticable.
		const body = await response.text().catch(() => "");
		throw new Error(
			`Brave Search HTTP ${response.status} ${response.statusText}${body ? `: ${body.slice(0, 500)}` : ""}`,
		);
	}

	const payload = (await response.json()) as {
		web?: { results?: Array<{ title?: string; url?: string; description?: string; age?: string }> };
	};

	return (payload.web?.results ?? []).map((r) => ({
		title: stripHtml(r.title ?? ""),
		url: r.url ?? "",
		description: stripHtml(r.description ?? ""),
		age: r.age,
	}));
}

export function formatResults(query: string, results: BraveResult[]): string {
	if (results.length === 0) return `Aucun résultat pour « ${query} ».`;

	return results
		.map((r, i) => {
			const lines = [`--- Résultat ${i + 1} ---`, `Titre : ${r.title}`, `Lien : ${r.url}`];
			if (r.age) lines.push(`Date : ${r.age}`);
			if (r.description) lines.push(`Extrait : ${r.description}`);
			return lines.join("\n");
		})
		.join("\n\n");
}

export default function (pi: ExtensionAPI) {
	pi.registerTool({
		name: "web_search",
		label: "Recherche web",
		description:
			"Recherche sur le web via Brave Search et retourne les résultats classés (titre, lien, extrait). " +
			"À utiliser pour vérifier un fait, trouver une référence de programme scolaire, une actualité, " +
			"ou une source récente que le modèle ne peut pas connaître.",
		promptSnippet: "Rechercher sur le web via Brave Search",
		promptGuidelines: [
			"Utilise web_search plutôt que de deviner quand une information est datée, chiffrée, ou susceptible d'avoir changé.",
			"Cite le lien retourné par web_search quand tu t'appuies sur un résultat.",
		],
		parameters: Type.Object({
			query: Type.String({ description: "La requête de recherche." }),
			count: Type.Optional(
				Type.Integer({
					minimum: 1,
					maximum: MAX_COUNT,
					description: `Nombre de résultats (défaut ${DEFAULT_COUNT}, max ${MAX_COUNT}).`,
				}),
			),
			freshness: Type.Optional(
				Type.String({
					description:
						"Filtre temporel : pd (24 h), pw (semaine), pm (mois), py (an), ou AAAA-MM-JJtoAAAA-MM-JJ.",
				}),
			),
		}),

		// Ne lève JAMAIS. Une exception ici serait remontée par pi comme un résultat
		// d'outil en erreur, ce qui termine le tour sur un échec technique au lieu
		// d'une réponse. Même contrat que brave.go côté Lya/Desk : tout échec
		// devient un texte dégradé qui invite le modèle à continuer.
		async execute(_toolCallId, params, signal) {
			const query = params.query?.trim() ?? "";
			if (!query) {
				return { content: [{ type: "text" as const, text: degraded("requête vide") }], details: {} };
			}

			const apiKey = readApiKey();
			if (!apiKey) {
				// L'outil reste enregistré même sans clé (l'extension se charge
				// toujours) ; buildPiPrompt s'abstient alors de l'annoncer. Si le
				// modèle l'appelle quand même, on dégrade proprement.
				return {
					content: [{ type: "text" as const, text: degraded(`${ENV_VAR} non configurée`) }],
					details: {},
				};
			}

			try {
				const results = await braveSearch({
					apiKey,
					query,
					count: params.count,
					freshness: params.freshness,
					signal: signal ?? undefined,
				});
				return {
					content: [{ type: "text" as const, text: formatResults(query, results) }],
					details: { query, count: results.length, results },
				};
			} catch (err) {
				const reason = err instanceof Error ? err.message : String(err);
				return {
					content: [{ type: "text" as const, text: degraded(reason) }],
					details: { query, error: reason },
				};
			}
		},
	});
}
