import { authoriseAdmin, authoriseUser } from "$lib/server/lucia"
import { fail } from "@sveltejs/kit"
import { prisma } from "$lib/server/prisma"
import ratelimit from "$lib/server/ratelimit"

// Make sure a user is an administrator before loading the page.
export async function load({ locals }) {
	await authoriseAdmin(locals)

	return {
		banners: prisma.announcements.findMany({
			include: {
				user: {
					select: {
						username: true,
						number: true,
					},
				},
			},
			orderBy: {
				id: "asc",
			},
		}),
	}
}

export const actions = {
	default: async ({ request, locals, getClientAddress }) => {
		await authoriseAdmin(locals)

		const { user } = await authoriseUser(locals.validateUser)

		const data = await request.formData()
		const action = data.get("action") as string
		const bannerId = data.get("id") as string

		const bannerActiveCount = await prisma.announcements.findMany({
			where: { active: true },
		})

		switch (action) {
			case "create":
				const limit = ratelimit("createBanner", getClientAddress, 30)
				if (limit) return limit

				const bannerText = data.get("bannerText") as string
				const bannerColour = data.get("bannerColour") as string
				const bannerTextLight = !!data.get("bannerTextLight")

				if (!bannerColour || !bannerText) return fail(400, { area: "create", msg: "Missing fields" })
				if (bannerText.length > 100 || bannerText.length < 3) return fail(400, { area: "create", msg: "Banner text too long" })

				if (bannerActiveCount && bannerActiveCount.length > 2) return fail(400, { area: "create", msg: "Too many active banners" })

				await prisma.announcements.create({
					data: {
						body: bannerText,
						bgColour: bannerColour,
						textLight: bannerTextLight,
						user: {
							connect: {
								id: user.userId,
							},
						},
					},
				})

				return {
					success: true,
					msg: "Banner created successfully!",
					area: "create",
				}
			case "show":
			case "hide":
				if (!bannerId) return fail(400, { msg: "Missing fields" })

				await prisma.announcements.update({
					where: {
						id: bannerId,
					},
					data: {
						active: action == "show",
					},
				})

				return
			case "delete":
				if (!bannerId) return fail(400, { msg: "Missing fields" })

				await prisma.announcements.delete({
					where: {
						id: bannerId,
					},
				})
				return
			case "updateBody":
				const bannerBody = data.get("bannerBody") as string

				if (!bannerBody || !bannerId) return fail(400, {area: "modal", msg: "Missing fields" })

				if (bannerBody.length < 3 || bannerBody.length > 99) return fail(400, {area: "modal", msg: "Banner text is too long/short" })

				await prisma.announcements.update({
					where: {
						id: bannerId,
					},
					data: {
						body: bannerBody,
					},
				})
				return {
					success: true,
					msg: "Banner updated successfully!",
					area: "modal",
					bannerBody,
				}
		}
	},
}