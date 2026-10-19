<?php
/**
 * Title: Texte et image
 * Slug: theme-desboussoles/texte-image
 * Categories: columns, media
 * Description: Colonne de texte et panneau de couleur illustré.
 * Viewport Width: 1200
 */
?>
<!-- wp:columns {"verticalAlignment":"center","align":"wide","style":{"spacing":{"blockGap":{"left":"var:preset|spacing|lg"}}}} -->
<div class="wp-block-columns alignwide are-vertically-aligned-center"><!-- wp:column {"verticalAlignment":"center","width":"55%"} -->
<div class="wp-block-column is-vertically-aligned-center" style="flex-basis:55%"><!-- wp:heading {"level":2,"style":{"typography":{"fontSize":"var:preset|font-size|h2"}}} -->
<h2 class="wp-block-heading" style="font-size:var(--wp--preset--font-size--h2)"><?php esc_html_e( 'Un titre accrocheur', 'theme-desboussoles' ); ?></h2>
<!-- /wp:heading -->

<!-- wp:paragraph -->
<p><?php esc_html_e( 'Deux ou trois phrases pour présenter votre idée. Le texte reste lisible : corps de texte à 18 px minimum, contraste maximal, phrases courtes.', 'theme-desboussoles' ); ?></p>
<!-- /wp:paragraph -->

<!-- wp:buttons -->
<div class="wp-block-buttons"><!-- wp:button -->
<div class="wp-block-button"><a class="wp-block-button__link wp-element-button"><?php esc_html_e( 'Découvrir', 'theme-desboussoles' ); ?></a></div>
<!-- /wp:button --></div>
<!-- /wp:buttons --></div>
<!-- /wp:column -->

<!-- wp:column {"verticalAlignment":"center","width":"45%"} -->
<div class="wp-block-column is-vertically-aligned-center" style="flex-basis:45%"><!-- wp:group {"className":"is-style-boussole-bandeau","backgroundColor":"fond-peche","style":{"spacing":{"padding":{"top":"var:preset|spacing|xxl","bottom":"var:preset|spacing|xxl","left":"var:preset|spacing|lg","right":"var:preset|spacing|lg"}}},"layout":{"type":"constrained"}} -->
<div class="wp-block-group is-style-boussole-bandeau has-fond-peche-background-color has-background" style="padding-top:var(--wp--preset--spacing--xxl);padding-right:var(--wp--preset--spacing--lg);padding-bottom:var(--wp--preset--spacing--xxl);padding-left:var(--wp--preset--spacing--lg)"><!-- wp:paragraph {"align":"center","style":{"typography":{"fontSize":"var:preset|font-size|body-lg","fontWeight":"800"}}} -->
<p class="has-text-align-center" style="font-size:var(--wp--preset--font-size--body-lg);font-weight:800"><?php esc_html_e( 'Visuel ou illustration', 'theme-desboussoles' ); ?></p>
<!-- /wp:paragraph --></div>
<!-- /wp:group --></div>
<!-- /wp:column --></div>
<!-- /wp:columns -->
