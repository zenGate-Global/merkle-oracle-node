package notifier

import (
	"fmt"

	"github.com/disgoorg/disgo/discord"
)

func BuildEmissionsMessage(
	baseURI string,
	amountToWithdraw int,
	txHash string,
	currentBlockHeight uint64,
	isEmissionsComplete bool,
) discord.WebhookMessageCreate {
	embed := discord.NewEmbedBuilder().
		SetColor(0xd9dadb).
		SetAuthor("palm economy", "", "https://pbs.twimg.com/profile_images/1821185843102412800/P23JRaEw_400x400.jpg").
		SetTitle(fmt.Sprintf("%s PALM Released", formatTokenAmount(amountToWithdraw))).
		SetURL(baseURI+txHash).
		SetThumbnail("https://i.imgur.com/WVQwpv1.png").
		SetFooter(fmt.Sprintf("Block %d", currentBlockHeight), "https://i.imgur.com/rzmSALh.png")

	if isEmissionsComplete {
		embed = embed.SetDescription("Emissions Complete")
	}

	return discord.NewWebhookMessageCreateBuilder().
		SetUsername("Zengate").
		SetAvatarURL("https://i.imgur.com/PCt3geP.png").
		SetEmbeds(embed.Build()).
		Build()
}
