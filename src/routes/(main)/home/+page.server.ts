import { authorise } from "$lib/server/lucia"
import { prisma, findPlaces } from "$lib/server/prisma"
import { roQuery } from "$lib/server/redis"
import ratelimit from "$lib/server/ratelimit"
import formError from "$lib/server/formError"
import { superValidate } from "sveltekit-superforms/server"
import { z } from "zod"

const schema = z.object({
	status: z.string().min(1).max(1000),
})

export async function load({ locals }) {
	console.time("home")
	const { user } = await authorise(locals)
	// (main)/+layout.server.ts will handle most redirects for logged-out users,
	// but sometimes errors for this page.

	async function Friends() {
		const friendsQuery = await roQuery(
			"friends",
			`
				MATCH (:User { name: $user }) -[r:friends]- (u:User)
				RETURN u.name as name
			`,
			{
				user: user.username,
			},
			false,
			true
		)

		let friends: any[] = []

		for (let i of friendsQuery || ([] as any)) {
			if (i.name) {
				const user = await prisma.authUser.findUnique({
					where: {
						username: i.name,
					},
					select: {
						username: true,
						number: true,
					},
				})
				if (user) friends.push(user)
			}
		}

		return friends
	}

	console.timeEnd("home")
	return {
		form: superValidate(schema),
		places: findPlaces({
			where: {
				privateServer: false,
			},
			select: {
				name: true,
				id: true,
				gameSessions: {
					where: {
						ping: {
							gt: Math.floor(Date.now() / 1000) - 35,
						},
					},
				},
			},
		}),
		friends: Friends(),
		feed: prisma.post.findMany({
			select: {
				id: true,
				content: true,
				posted: true,
				authorUser: {
					select: {
						username: true,
						number: true,
					},
				},
			},
			orderBy: {
				posted: "desc",
			},
			take: 40,
		}),
	}
}

export const actions = {
	default: async ({ request, locals, getClientAddress }) => {
		const form = await superValidate(request, schema)
		if (!form.valid) return formError(form)
		const limit = ratelimit(form, "statusPost", getClientAddress, 30)
		if (limit) return limit

		const { user } = await authorise(locals)

		await prisma.post.create({
			data: {
				authorUser: {
					connect: {
						username: user.username,
					},
				},
				content: form.data.status,
			},
		})
	},
}
